// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestOTLPHTTPEndpointPaths(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	for _, tc := range []struct {
		name            string
		signalEndpoint  string
		genericEndpoint string
		optionURL       string
		optionPath      string
		wantHost        string
		wantPath        string
	}{
		{name: "signal custom path", signalEndpoint: "http://signal.invalid:4318/custom", wantHost: "signal.invalid:4318", wantPath: "/custom"},
		{name: "signal standard path", signalEndpoint: "http://signal.invalid:4318/v1/logs", wantHost: "signal.invalid:4318", wantPath: "/v1/logs"},
		{name: "signal no path", signalEndpoint: "http://signal.invalid:4318", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "signal root", signalEndpoint: "http://signal.invalid:4318/", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "signal trailing slash", signalEndpoint: "http://signal.invalid:4318/custom/", wantHost: "signal.invalid:4318", wantPath: "/custom/"},
		{name: "generic no path", genericEndpoint: "http://generic.invalid:4318", wantHost: "generic.invalid:4318", wantPath: "/v1/logs"},
		{name: "generic base path", genericEndpoint: "http://generic.invalid:4318/collector", wantHost: "generic.invalid:4318", wantPath: "/collector/v1/logs"},
		{name: "generic repeated slashes", genericEndpoint: "http://generic.invalid:4318/tenant//collector", wantHost: "generic.invalid:4318", wantPath: "/tenant//collector/v1/logs"},
		{name: "generic dot segment", genericEndpoint: "http://generic.invalid:4318/tenant/./collector", wantHost: "generic.invalid:4318", wantPath: "/tenant/./collector/v1/logs"},
		{name: "generic parent segment", genericEndpoint: "http://generic.invalid:4318/tenant/../collector", wantHost: "generic.invalid:4318", wantPath: "/tenant/../collector/v1/logs"},
		{name: "generic repeated trailing slashes", genericEndpoint: "http://generic.invalid:4318/collector//", wantHost: "generic.invalid:4318", wantPath: "/collector//v1/logs"},
		{name: "generic trailing slash", genericEndpoint: "http://generic.invalid:4318/collector/", wantHost: "generic.invalid:4318", wantPath: "/collector/v1/logs"},
		{name: "generic standard path is still a base", genericEndpoint: "http://generic.invalid:4318/v1/logs", wantHost: "generic.invalid:4318", wantPath: "/v1/logs/v1/logs"},
		{name: "signal precedence", signalEndpoint: "http://signal.invalid:4318/custom", genericEndpoint: "http://generic.invalid:4318/ignored", wantHost: "signal.invalid:4318", wantPath: "/custom"},
		{name: "signal root precedence", signalEndpoint: "http://signal.invalid:4318", genericEndpoint: "http://generic.invalid:4318/ignored", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "same URL in both sources", signalEndpoint: "http://signal.invalid:4318", genericEndpoint: "http://signal.invalid:4318", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "agent fallback", wantHost: "agent.invalid:4318", wantPath: "/v1/logs"},
		{name: "invalid signal fallback", signalEndpoint: "http://%invalid", genericEndpoint: "http://generic.invalid:4318/collector", wantHost: "agent.invalid:4318", wantPath: "/v1/logs"},
		{name: "invalid generic fallback", genericEndpoint: "http://%invalid", wantHost: "agent.invalid:4318", wantPath: "/v1/logs"},
		{name: "explicit URL overrides both", signalEndpoint: "http://signal.invalid:4318/ignored", genericEndpoint: "http://generic.invalid:4318/ignored", optionURL: "http://explicit.invalid:4318/option", wantHost: "explicit.invalid:4318", wantPath: "/option"},
		{name: "explicit path overrides signal", signalEndpoint: "http://signal.invalid:4318/ignored", optionPath: "/option", wantHost: "signal.invalid:4318", wantPath: "/option"},
		{name: "explicit URL overrides agent", optionURL: "http://explicit.invalid:4318/option", wantHost: "explicit.invalid:4318", wantPath: "/option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", tc.signalEndpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.genericEndpoint)
			t.Setenv("DD_TRACE_AGENT_URL", "http://agent.invalid:8126")
			t.Setenv("DD_AGENT_HOST", "unused-agent.invalid")
			requests := make(chan *http.Request, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r
				w.Header().Set("Content-Type", "application/x-protobuf")
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)
			// Dial the local collector without changing the configured destination or request path.
			transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			}}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			config.CreateNew()
			// The shared configuration must retain which endpoint source won.
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://changed.invalid/ignored")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://changed.invalid/ignored")
			opts := []otlploghttp.Option{otlploghttp.WithHTTPClient(client)}
			if tc.optionURL != "" {
				opts = append(opts, otlploghttp.WithEndpointURL(tc.optionURL))
			}
			if tc.optionPath != "" {
				opts = append(opts, otlploghttp.WithURLPath(tc.optionPath))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			exp, err := newOTLPHTTPExporter(ctx, opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })
			require.NoError(t, exp.Export(ctx, []sdklog.Record{{}}))
			select {
			case request := <-requests:
				assert.Equal(t, http.MethodPost, request.Method)
				assert.Equal(t, tc.wantHost, request.Host)
				assert.Equal(t, tc.wantPath, request.URL.EscapedPath())
			default:
				t.Fatal("export succeeded without an HTTP request")
			}
		})
	}
}

func TestOTLPGRPCEndpointPrecedence(t *testing.T) {
	t.Cleanup(func() { config.CreateNew() })
	for _, tc := range []struct {
		name            string
		signalEndpoint  string
		genericEndpoint string
		optionEndpoint  string
		wantEndpoint    string
	}{
		{name: "signal", signalEndpoint: "http://127.0.0.2:4317/custom", wantEndpoint: "127.0.0.2:4317"},
		{name: "generic", genericEndpoint: "http://127.0.0.3:4317/base", wantEndpoint: "127.0.0.3:4317"},
		{name: "signal precedence", signalEndpoint: "http://127.0.0.2:4317", genericEndpoint: "http://127.0.0.3:4317", wantEndpoint: "127.0.0.2:4317"},
		{name: "agent fallback", wantEndpoint: "127.0.0.4:4317"},
		{name: "invalid signal fallback", signalEndpoint: "http://%invalid", genericEndpoint: "http://127.0.0.3:4317", wantEndpoint: "127.0.0.4:4317"},
		{name: "explicit option", signalEndpoint: "http://127.0.0.2:4317", genericEndpoint: "http://127.0.0.3:4317", optionEndpoint: "127.0.0.5:4317", wantEndpoint: "127.0.0.5:4317"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", tc.signalEndpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.genericEndpoint)
			t.Setenv("DD_TRACE_AGENT_URL", "http://127.0.0.4:8126")
			config.CreateNew()
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://127.0.0.6:4317")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			srv := grpc.NewServer()
			collector := &endpointLogsCollector{requests: make(chan *collectorlog.ExportLogsServiceRequest, 1)}
			collectorlog.RegisterLogsServiceServer(srv, collector)
			go func() { _ = srv.Serve(listener) }()
			t.Cleanup(srv.Stop)
			targets := make(chan string, 1)
			opts := []otlploggrpc.Option{otlploggrpc.WithDialOption(grpc.WithContextDialer(func(ctx context.Context, target string) (net.Conn, error) {
				select {
				case targets <- target:
				default:
				}
				return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
			}))}
			if tc.optionEndpoint != "" {
				opts = append(opts, otlploggrpc.WithEndpoint(tc.optionEndpoint))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			exp, err := newOTLPGRPCExporter(ctx, opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })
			require.NoError(t, exp.Export(ctx, []sdklog.Record{{}}))
			select {
			case req := <-collector.requests:
				require.Len(t, req.ResourceLogs, 1)
			default:
				t.Fatal("export succeeded without a gRPC request")
			}
			assert.Equal(t, tc.wantEndpoint, <-targets)
		})
	}
}

type endpointLogsCollector struct {
	collectorlog.UnimplementedLogsServiceServer
	requests chan *collectorlog.ExportLogsServiceRequest
}

func (c *endpointLogsCollector) Export(_ context.Context, req *collectorlog.ExportLogsServiceRequest) (*collectorlog.ExportLogsServiceResponse, error) {
	c.requests <- req
	return &collectorlog.ExportLogsServiceResponse{}, nil
}
