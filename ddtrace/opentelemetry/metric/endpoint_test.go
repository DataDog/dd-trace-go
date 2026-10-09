// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestOTLPHTTPEndpointPaths(t *testing.T) {
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
		{name: "signal standard path", signalEndpoint: "http://signal.invalid:4318/v1/metrics", wantHost: "signal.invalid:4318", wantPath: "/v1/metrics"},
		{name: "signal no path", signalEndpoint: "http://signal.invalid:4318", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "signal root", signalEndpoint: "http://signal.invalid:4318/", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "signal trailing slash", signalEndpoint: "http://signal.invalid:4318/custom/", wantHost: "signal.invalid:4318", wantPath: "/custom/"},
		{name: "generic no path", genericEndpoint: "http://generic.invalid:4318", wantHost: "generic.invalid:4318", wantPath: "/v1/metrics"},
		{name: "generic base path", genericEndpoint: "http://generic.invalid:4318/collector", wantHost: "generic.invalid:4318", wantPath: "/collector/v1/metrics"},
		{name: "generic trailing slash", genericEndpoint: "http://generic.invalid:4318/collector/", wantHost: "generic.invalid:4318", wantPath: "/collector/v1/metrics"},
		{name: "generic standard path is still a base", genericEndpoint: "http://generic.invalid:4318/v1/metrics", wantHost: "generic.invalid:4318", wantPath: "/v1/metrics/v1/metrics"},
		{name: "signal precedence", signalEndpoint: "http://signal.invalid:4318/custom", genericEndpoint: "http://generic.invalid:4318/ignored", wantHost: "signal.invalid:4318", wantPath: "/custom"},
		{name: "signal root precedence", signalEndpoint: "http://signal.invalid:4318", genericEndpoint: "http://generic.invalid:4318/ignored", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "same URL in both sources", signalEndpoint: "http://signal.invalid:4318", genericEndpoint: "http://signal.invalid:4318", wantHost: "signal.invalid:4318", wantPath: "/"},
		{name: "agent fallback", wantHost: "agent.invalid:4318", wantPath: "/v1/metrics"},
		{name: "explicit URL overrides both", signalEndpoint: "http://signal.invalid:4318/ignored", genericEndpoint: "http://generic.invalid:4318/ignored", optionURL: "http://explicit.invalid:4318/option", wantHost: "explicit.invalid:4318", wantPath: "/option"},
		{name: "explicit path overrides signal", signalEndpoint: "http://signal.invalid:4318/ignored", optionPath: "/option", wantHost: "signal.invalid:4318", wantPath: "/option"},
		{name: "explicit URL overrides agent", optionURL: "http://explicit.invalid:4318/option", wantHost: "explicit.invalid:4318", wantPath: "/option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", tc.signalEndpoint)
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
			opts := []otlpmetrichttp.Option{otlpmetrichttp.WithHTTPClient(client)}
			if tc.optionURL != "" {
				opts = append(opts, otlpmetrichttp.WithEndpointURL(tc.optionURL))
			}
			if tc.optionPath != "" {
				opts = append(opts, otlpmetrichttp.WithURLPath(tc.optionPath))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			exp, err := newDatadogOTLPHTTPExporter(ctx, opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })
			require.NoError(t, exp.Export(ctx, &metricdata.ResourceMetrics{}))
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
