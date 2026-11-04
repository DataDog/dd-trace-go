// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestOTLPHTTPEndpointPaths(t *testing.T) {
	t.Cleanup(func() { internalconfig.CreateNew() })
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/protobuf")
	for _, tc := range []struct {
		name            string
		signalEndpoint  string
		genericEndpoint string
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
		{name: "invalid signal fallback", signalEndpoint: "http://%invalid", genericEndpoint: "http://generic.invalid:4318/collector", wantHost: "generic.invalid:4318", wantPath: "/collector/v1/metrics"},
		{name: "invalid generic fallback", genericEndpoint: "http://%invalid", wantHost: "agent.invalid:4318", wantPath: "/v1/metrics"},
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
			exp := newOTLPMetricsExporter(internalconfig.CreateNew())
			exp.transport.client = client
			stats := &pb.ClientGroupedStats{Service: "svc", Resource: "request", OkSummary: encodeSketch(t, 50e6)}
			require.NoError(t, exp.export(makePayload("svc", "", "", []*pb.ClientGroupedStats{stats})))
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
