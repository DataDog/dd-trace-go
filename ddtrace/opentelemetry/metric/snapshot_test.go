// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	collectormetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestBuildDatadogResourceFromSnapshotDoesNotResampleEnvironment(t *testing.T) {
	t.Setenv("DD_SERVICE", "captured")
	snapshot := internalconfig.ResolveOTelMetricSnapshot()
	t.Setenv("DD_SERVICE", "changed-after-snapshot")

	res, err := buildDatadogResourceFromSnapshot(context.Background(), snapshot)
	require.NoError(t, err)

	value, ok := res.Set().Value(semconv.ServiceNameKey)
	require.True(t, ok)
	assert.Equal(t, "captured", value.AsString())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func successfulResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/x-protobuf"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestMetricExporterTimeoutRemainsThirtySeconds(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TIMEOUT", "2")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	exporter, err := newDatadogOTLPHTTPExporter(
		context.Background(),
		otlpmetrichttp.WithEndpointURL(server.URL),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	started := time.Now()
	require.NoError(t, exporter.Export(context.Background(), new(metricdata.ResourceMetrics)))
	assert.GreaterOrEqual(t, time.Since(started), 50*time.Millisecond,
		"the explicit thirty-second timeout must override the one- and two-millisecond environment values")
}

func TestMetricExporterPreservesSDKEndpointAndHeaderParsing(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://generic.example/base/")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://metrics.example/custom")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "generic=ignored")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", " x-token =first%20value,x-token=last%20value,bad key=secret")
	snapshot := internalconfig.ResolveOTelMetricSnapshot()
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://changed.example/leaked")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "changed=leaked")

	requests := make(chan *http.Request, 1)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests <- request.Clone(request.Context())
		return successfulResponse(), nil
	})}
	exporter, err := newDatadogOTLPHTTPExporterFromSnapshot(
		context.Background(),
		snapshot,
		otlpmetrichttp.WithHTTPClient(client),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	require.NoError(t, exporter.Export(context.Background(), new(metricdata.ResourceMetrics)))
	request := <-requests
	assert.Equal(t, "metrics.example", request.URL.Host)
	assert.Equal(t, "/custom", request.URL.Path,
		"a signal-specific endpoint path is used as-is")
	assert.Equal(t, "last value", request.Header.Get("x-token"))
	assert.Empty(t, request.Header.Get("generic"),
		"signal-specific headers replace generic headers")
	assert.Empty(t, request.Header.Get("bad key"),
		"the SDK rejects invalid header token names")
	assert.Empty(t, request.Header.Get("changed"),
		"the exporter must not resample headers after its constructor snapshot")
}

func TestMetricExporterCapturedEmptyHeadersBlockLaterEnvironment(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "")
	snapshot := internalconfig.ResolveOTelMetricSnapshot()
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://changed.example/leaked")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "changed=leaked")

	requests := make(chan *http.Request, 1)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests <- request.Clone(request.Context())
		return successfulResponse(), nil
	})}
	exporter, err := newDatadogOTLPHTTPExporterFromSnapshot(
		context.Background(),
		snapshot,
		otlpmetrichttp.WithHTTPClient(client),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

	require.NoError(t, exporter.Export(context.Background(), new(metricdata.ResourceMetrics)))
	request := <-requests
	assert.Equal(t, "localhost:4318", request.URL.Host)
	assert.Empty(t, request.Header.Get("changed"))
}

func TestMetricHTTPExporterPreservesCapturedInsecurePrecedence(t *testing.T) {
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
			name:           "empty signal leaves generic value",
			endpoint:       "http://captured.example",
			generic:        "false",
			signal:         "   ",
			wantScheme:     "https",
			mutatedGeneric: "true",
			mutatedSignal:  "true",
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
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", tt.signal)
			t.Setenv("DD_TRACE_AGENT_URL", tt.ddAgentURL)
			t.Setenv("DD_AGENT_HOST", "")
			snapshot := internalconfig.ResolveOTelMetricSnapshot()
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.mutatedGeneric)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", tt.mutatedSignal)

			requests := make(chan *http.Request, 1)
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests <- request.Clone(request.Context())
				return successfulResponse(), nil
			})}
			opts := []otlpmetrichttp.Option{otlpmetrichttp.WithHTTPClient(client)}
			if tt.userEndpoint != "" {
				opts = append(opts, otlpmetrichttp.WithEndpointURL(tt.userEndpoint))
			}
			exporter, err := newDatadogOTLPHTTPExporterFromSnapshot(
				context.Background(),
				snapshot,
				opts...,
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

			require.NoError(t, exporter.Export(context.Background(), new(metricdata.ResourceMetrics)))
			assert.Equal(t, tt.wantScheme, (<-requests).URL.Scheme)
		})
	}
}

type plaintextMetricCollector struct {
	collectormetricpb.UnimplementedMetricsServiceServer
}

func (plaintextMetricCollector) Export(
	context.Context,
	*collectormetricpb.ExportMetricsServiceRequest,
) (*collectormetricpb.ExportMetricsServiceResponse, error) {
	return new(collectormetricpb.ExportMetricsServiceResponse), nil
}

func TestMetricGRPCExporterPreservesCapturedInsecurePrecedence(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	collectormetricpb.RegisterMetricsServiceServer(server, plaintextMetricCollector{})
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
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", tt.signal)
			t.Setenv("DD_TRACE_AGENT_URL", tt.ddAgentURL)
			t.Setenv("DD_AGENT_HOST", "")
			snapshot := internalconfig.ResolveOTelMetricSnapshot()
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.mutatedGeneric)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", tt.mutatedSignal)

			opts := []otlpmetricgrpc.Option{
				otlpmetricgrpc.WithEndpoint("passthrough:///bufnet"),
				otlpmetricgrpc.WithDialOption(grpc.WithContextDialer(
					func(context.Context, string) (net.Conn, error) {
						return listener.Dial()
					},
				)),
				otlpmetricgrpc.WithTimeout(250 * time.Millisecond),
				otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{Enabled: false}),
			}
			if tt.userInsecure {
				opts = append(opts, otlpmetricgrpc.WithInsecure())
			}
			exporter, err := newDatadogOTLPGRPCExporterFromSnapshot(
				context.Background(),
				snapshot,
				opts...,
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })

			err = exporter.Export(context.Background(), new(metricdata.ResourceMetrics))
			if tt.wantPlaintext {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "a secure exporter must not send plaintext to the test collector")
			}
		})
	}
}

func TestMetricCapturedEndpointMatchesSDKFallbackRules(t *testing.T) {
	raw := func(value string) internalconfig.OTelRawSetting {
		return internalconfig.OTelRawSetting{Value: value, Present: true, Valid: true}
	}
	tests := []struct {
		name       string
		generic    string
		signal     string
		wantHTTP   string
		wantGRPC   string
		wantTarget string
	}{
		{
			name:       "generic appends signal path",
			generic:    "http://generic.example/base/",
			wantHTTP:   "http://generic.example/base/v1/metrics",
			wantGRPC:   "http://generic.example/base/",
			wantTarget: "generic.example/base",
		},
		{
			name:       "signal path is exact",
			generic:    "http://generic.example/base",
			signal:     "https://signal.example",
			wantHTTP:   "https://signal.example/",
			wantGRPC:   "https://signal.example",
			wantTarget: "signal.example",
		},
		{
			name:       "invalid signal keeps valid generic",
			generic:    "http://generic.example/base",
			signal:     "https://bad.example/%",
			wantHTTP:   "http://generic.example/base/v1/metrics",
			wantGRPC:   "http://generic.example/base",
			wantTarget: "generic.example/base",
		},
		{
			name:       "whitespace signal keeps valid generic",
			generic:    "http://generic.example/base",
			signal:     "   ",
			wantHTTP:   "http://generic.example/base/v1/metrics",
			wantGRPC:   "http://generic.example/base",
			wantTarget: "generic.example/base",
		},
		{
			name:       "invalid configured endpoint uses SDK default",
			signal:     "https://bad.example/%",
			wantHTTP:   "https://localhost:4318/v1/metrics",
			wantGRPC:   "https://localhost:4317",
			wantTarget: "localhost:4317",
		},
		{
			name:       "whitespace configured endpoint uses SDK default",
			signal:     "   ",
			wantHTTP:   "https://localhost:4318/v1/metrics",
			wantGRPC:   "https://localhost:4317",
			wantTarget: "localhost:4317",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := internalconfig.OTelMetricSnapshot{}
			if tt.generic != "" {
				snapshot.Generic.Endpoint = raw(tt.generic)
			}
			if tt.signal != "" {
				snapshot.Signal.Endpoint = raw(tt.signal)
			}
			assert.Equal(t, tt.wantHTTP, metricHTTPEndpointURLFromSnapshot(snapshot))
			grpcURL, grpcTarget := metricGRPCEndpointFromSnapshot(snapshot)
			assert.Equal(t, tt.wantGRPC, grpcURL)
			assert.Equal(t, tt.wantTarget, grpcTarget)
		})
	}
}

func TestMetricUserOptionsRetainPrecedenceOverSnapshotDefaults(t *testing.T) {
	t.Setenv("DD_SERVICE", "datadog-service")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "1000")
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "2000")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")
	snapshot := internalconfig.ResolveOTelMetricSnapshot()

	cfg := newConfig(snapshot)
	for _, option := range []Option{
		WithExportInterval(3 * time.Second),
		WithExportTimeout(4 * time.Second),
		WithCumulativeTemporality(),
	} {
		option.apply(cfg)
	}
	assert.Equal(t, 3*time.Second, cfg.exportInterval)
	assert.Equal(t, 4*time.Second, cfg.exportTimeout)
	require.NotNil(t, cfg.temporalitySelector)
	assert.Equal(t,
		metricdata.CumulativeTemporality,
		cfg.temporalitySelector(sdkmetric.InstrumentKindCounter),
	)

	res, err := buildDatadogResourceFromSnapshot(
		context.Background(),
		snapshot,
		resource.WithAttributes(
			semconv.ServiceName("user-service"),
			attribute.String("user.attribute", "preserved"),
		),
	)
	require.NoError(t, err)
	service, ok := res.Set().Value(semconv.ServiceNameKey)
	require.True(t, ok)
	assert.Equal(t, "datadog-service", service.AsString(),
		"Datadog resource attributes retain their existing precedence over user resource options")
	userAttribute, ok := res.Set().Value(attribute.Key("user.attribute"))
	require.True(t, ok)
	assert.Equal(t, "preserved", userAttribute.AsString())
}
