// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	collectorlogpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestBuildResourceFromSnapshotDoesNotResampleEnvironment(t *testing.T) {
	t.Setenv("DD_SERVICE", "captured")
	snapshot := internalconfig.ResolveOTelLogSnapshot()
	t.Setenv("DD_SERVICE", "changed-after-snapshot")

	res, err := buildResourceFromSnapshot(context.Background(), snapshot)
	require.NoError(t, err)

	value, ok := res.Set().Value(semconv.ServiceNameKey)
	require.True(t, ok)
	assert.Equal(t, "captured", value.AsString())
}

type logRoundTripFunc func(*http.Request) (*http.Response, error)

func (f logRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func logSuccessfulResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/x-protobuf"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestLogExporterDoesNotResampleEndpointOrHeaders(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://captured.example/base/")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "captured=one")
	snapshot := internalconfig.ResolveOTelLogSnapshot()
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://changed.example/leaked")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "changed=leaked")

	requests := make(chan *http.Request, 1)
	client := &http.Client{Transport: logRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests <- request.Clone(request.Context())
		return logSuccessfulResponse(), nil
	})}
	exporter, err := newOTLPHTTPExporterFromSnapshot(
		context.Background(),
		snapshot,
		otlploghttp.WithHTTPClient(client),
		otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	require.NoError(t, exporter.Export(context.Background(), []sdklog.Record{{}}))
	request := <-requests
	assert.Equal(t, "captured.example", request.URL.Host)
	assert.Equal(t, "/base/v1/logs", request.URL.Path)
	assert.Equal(t, "one", request.Header.Get("captured"))
	assert.Empty(t, request.Header.Get("changed"))
}

func TestLogExporterCapturedEmptyHeadersBlockLaterEnvironment(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "")
	snapshot := internalconfig.ResolveOTelLogSnapshot()
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://changed.example/leaked")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "changed=leaked")

	requests := make(chan *http.Request, 1)
	client := &http.Client{Transport: logRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests <- request.Clone(request.Context())
		return logSuccessfulResponse(), nil
	})}
	exporter, err := newOTLPHTTPExporterFromSnapshot(
		context.Background(),
		snapshot,
		otlploghttp.WithHTTPClient(client),
		otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	require.NoError(t, exporter.Export(context.Background(), []sdklog.Record{{}}))
	request := <-requests
	assert.Equal(t, "localhost:4318", request.URL.Host)
	assert.Empty(t, request.Header.Get("changed"))
}

func TestLogHTTPExporterPreservesCapturedInsecurePrecedence(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		ddAgentURL     string
		generic        string
		signal         string
		wantScheme     string
		userEndpoint   string
		mutatedGeneric string
		mutatedSignal  string
	}{
		{
			name:           "generic true overrides HTTPS endpoint",
			endpoint:       "https://captured.example",
			generic:        "true",
			wantScheme:     "http",
			mutatedGeneric: "false",
		},
		{
			name:           "generic false overrides HTTP endpoint",
			endpoint:       "http://captured.example",
			generic:        "false",
			wantScheme:     "https",
			mutatedGeneric: "true",
		},
		{
			name:           "signal false overrides generic true",
			endpoint:       "http://captured.example",
			generic:        "true",
			signal:         "false",
			wantScheme:     "https",
			mutatedGeneric: "true",
			mutatedSignal:  "true",
		},
		{
			name:           "signal true overrides generic false",
			endpoint:       "https://captured.example",
			generic:        "false",
			signal:         "true",
			wantScheme:     "http",
			mutatedGeneric: "false",
			mutatedSignal:  "false",
		},
		{
			name:           "non-true value means secure",
			endpoint:       "http://captured.example",
			generic:        "not-a-bool",
			wantScheme:     "https",
			mutatedGeneric: "true",
		},
		{
			name:           "generic true applies before secure DD fallback",
			ddAgentURL:     "https://agent.example:8126",
			generic:        "true",
			wantScheme:     "http",
			mutatedGeneric: "false",
		},
		{
			name:           "generic false applies before secure DD fallback",
			ddAgentURL:     "https://agent.example:8126",
			generic:        "false",
			wantScheme:     "https",
			mutatedGeneric: "true",
		},
		{
			name:           "plaintext DD fallback wins generic false",
			ddAgentURL:     "http://agent.example:8126",
			generic:        "false",
			wantScheme:     "http",
			mutatedGeneric: "true",
		},
		{
			name:           "user endpoint remains last",
			endpoint:       "https://captured.example",
			generic:        "true",
			wantScheme:     "https",
			userEndpoint:   "https://user.example/custom",
			mutatedGeneric: "false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", tt.signal)
			t.Setenv("DD_TRACE_AGENT_URL", tt.ddAgentURL)
			t.Setenv("DD_AGENT_HOST", "")
			snapshot := internalconfig.ResolveOTelLogSnapshot()
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.mutatedGeneric)
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", tt.mutatedSignal)

			requests := make(chan *http.Request, 1)
			client := &http.Client{Transport: logRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests <- request.Clone(request.Context())
				return logSuccessfulResponse(), nil
			})}
			opts := []otlploghttp.Option{
				otlploghttp.WithHTTPClient(client),
				otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false}),
			}
			if tt.userEndpoint != "" {
				opts = append(opts, otlploghttp.WithEndpointURL(tt.userEndpoint))
			}
			exporter, err := newOTLPHTTPExporterFromSnapshot(
				context.Background(),
				snapshot,
				opts...,
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

			require.NoError(t, exporter.Export(context.Background(), []sdklog.Record{{}}))
			assert.Equal(t, tt.wantScheme, (<-requests).URL.Scheme)
		})
	}
}

type plaintextLogCollector struct {
	collectorlogpb.UnimplementedLogsServiceServer
}

func (plaintextLogCollector) Export(
	context.Context,
	*collectorlogpb.ExportLogsServiceRequest,
) (*collectorlogpb.ExportLogsServiceResponse, error) {
	return new(collectorlogpb.ExportLogsServiceResponse), nil
}

func TestLogGRPCExporterPreservesCapturedInsecurePrecedence(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	collectorlogpb.RegisterLogsServiceServer(server, plaintextLogCollector{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	tests := []struct {
		name           string
		endpoint       string
		ddAgentURL     string
		generic        string
		signal         string
		wantPlaintext  bool
		userInsecure   bool
		mutatedGeneric string
		mutatedSignal  string
	}{
		{
			name:           "generic true overrides HTTPS endpoint",
			endpoint:       "https://captured.example",
			generic:        "true",
			wantPlaintext:  true,
			mutatedGeneric: "false",
		},
		{
			name:           "generic false overrides HTTP endpoint",
			endpoint:       "http://captured.example",
			generic:        "false",
			wantPlaintext:  false,
			mutatedGeneric: "true",
		},
		{
			name:           "signal true overrides generic false",
			endpoint:       "https://captured.example",
			generic:        "false",
			signal:         "true",
			wantPlaintext:  true,
			mutatedGeneric: "false",
			mutatedSignal:  "false",
		},
		{
			name:           "signal false overrides generic true",
			endpoint:       "http://captured.example",
			generic:        "true",
			signal:         "false",
			wantPlaintext:  false,
			mutatedGeneric: "true",
			mutatedSignal:  "true",
		},
		{
			name:           "generic true applies before secure DD fallback",
			ddAgentURL:     "https://agent.example:8126",
			generic:        "true",
			wantPlaintext:  true,
			mutatedGeneric: "false",
		},
		{
			name:           "generic false applies before secure DD fallback",
			ddAgentURL:     "https://agent.example:8126",
			generic:        "false",
			wantPlaintext:  false,
			mutatedGeneric: "true",
		},
		{
			name:           "plaintext DD fallback wins generic false",
			ddAgentURL:     "http://agent.example:8126",
			generic:        "false",
			wantPlaintext:  true,
			mutatedGeneric: "true",
		},
		{
			name:           "user insecure option remains last",
			endpoint:       "http://captured.example",
			generic:        "false",
			wantPlaintext:  true,
			userInsecure:   true,
			mutatedGeneric: "true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", tt.signal)
			t.Setenv("DD_TRACE_AGENT_URL", tt.ddAgentURL)
			t.Setenv("DD_AGENT_HOST", "")
			snapshot := internalconfig.ResolveOTelLogSnapshot()
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.mutatedGeneric)
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_INSECURE", tt.mutatedSignal)

			opts := []otlploggrpc.Option{
				otlploggrpc.WithEndpoint("passthrough:///bufnet"),
				otlploggrpc.WithDialOption(grpc.WithContextDialer(
					func(context.Context, string) (net.Conn, error) {
						return listener.Dial()
					},
				)),
				otlploggrpc.WithTimeout(250 * time.Millisecond),
				otlploggrpc.WithRetry(otlploggrpc.RetryConfig{Enabled: false}),
			}
			if tt.userInsecure {
				opts = append(opts, otlploggrpc.WithInsecure())
			}
			exporter, err := newOTLPGRPCExporterFromSnapshot(
				context.Background(),
				snapshot,
				opts...,
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

			err = exporter.Export(context.Background(), []sdklog.Record{{}})
			if tt.wantPlaintext {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "a secure exporter must not send plaintext to the test collector")
			}
		})
	}
}

type reentrantLoggerTelemetryClient struct {
	*telemetrytest.RecordClient
	reentered chan struct{}
	provider  chan bool
	initErr   chan error
}

func (c *reentrantLoggerTelemetryClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	select {
	case <-c.reentered:
	default:
		close(c.reentered)
		c.provider <- GetGlobalLoggerProvider() != nil
		c.initErr <- InitGlobalLoggerProvider(context.Background())
	}
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestInitGlobalLoggerProviderPublishesBeforeReentrantTelemetry(t *testing.T) {
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	require.NoError(t, ShutdownGlobalLoggerProvider(context.Background()))

	client := &reentrantLoggerTelemetryClient{
		RecordClient: new(telemetrytest.RecordClient),
		reentered:    make(chan struct{}),
		provider:     make(chan bool, 1),
		initErr:      make(chan error, 1),
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan error, 1)
	go func() {
		done <- InitGlobalLoggerProvider(context.Background())
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("logger initialization blocked while configuration telemetry reentered the provider")
	}
	select {
	case published := <-client.provider:
		assert.True(t, published, "the provider must be visible before configuration telemetry")
	case <-time.After(time.Second):
		t.Fatal("logger initialization did not report configuration telemetry")
	}
	select {
	case err := <-client.initErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("logger initialization telemetry could not reenter initialization")
	}
	require.NoError(t, ShutdownGlobalLoggerProvider(context.Background()))
}
