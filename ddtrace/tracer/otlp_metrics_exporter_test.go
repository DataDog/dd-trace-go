// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	otlpcollectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

// captureServer creates a test HTTP server that records the last request it received.
type captureServer struct {
	*httptest.Server
	lastBody        []byte
	lastContentType string
}

func newCaptureServer(t *testing.T) *captureServer {
	t.Helper()
	cs := &captureServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.lastContentType = r.Header.Get("Content-Type")
		cs.lastBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cs.Server.Close)
	return cs
}

func makeExporterWithServer(t *testing.T, srv *captureServer, protocol string) *otlpMetricsExporter {
	t.Helper()
	cfg := internalconfig.CreateNew()
	return &otlpMetricsExporter{
		client:   srv.Server.Client(),
		url:      srv.URL + "/v1/metrics",
		headers:  nil,
		protocol: protocol,
		cfg:      cfg,
	}
}

// ---- otlpMetricsExporter.export ----

func TestOTLPMetricsExporterExportEmptyPayload(t *testing.T) {
	srv := newCaptureServer(t)
	exp := makeExporterWithServer(t, srv, "http/json")
	// A payload with no groups yields a nil request; no HTTP call is made.
	err := exp.export(makePayload("svc", "", "", nil))
	require.NoError(t, err)
	assert.Empty(t, srv.lastBody, "no HTTP call expected for empty payload")
}

func TestOTLPMetricsExporterExportJSONContentType(t *testing.T) {
	srv := newCaptureServer(t)
	exp := makeExporterWithServer(t, srv, "http/json")
	gs := &pb.ClientGroupedStats{
		Service:   "svc",
		Resource:  "web.request",
		OkSummary: encodeSketch(t, 50e6),
	}
	err := exp.export(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}))
	require.NoError(t, err)
	assert.Equal(t, otlpMetricsContentTypeJSON, srv.lastContentType)
	assert.NotEmpty(t, srv.lastBody)
}

func TestOTLPMetricsExporterExportProtoContentType(t *testing.T) {
	srv := newCaptureServer(t)
	exp := makeExporterWithServer(t, srv, "http/protobuf")
	gs := &pb.ClientGroupedStats{
		Service:   "svc",
		Resource:  "web.request",
		OkSummary: encodeSketch(t, 50e6),
	}
	err := exp.export(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}))
	require.NoError(t, err)
	assert.Equal(t, otlpMetricsContentTypeProto, srv.lastContentType)
	assert.NotEmpty(t, srv.lastBody)
}

func TestOTLPMetricsExporterExportJSONIsValidOTLP(t *testing.T) {
	srv := newCaptureServer(t)
	exp := makeExporterWithServer(t, srv, "http/json")
	gs := &pb.ClientGroupedStats{
		Service:   "svc",
		Resource:  "web.request",
		OkSummary: encodeSketch(t, 50e6),
	}
	require.NoError(t, exp.export(makePayload("svc", "prod", "1.0", []*pb.ClientGroupedStats{gs})))

	// The body must be valid JSON with the expected metric name.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(srv.lastBody, &parsed))
	body := string(srv.lastBody)
	assert.Contains(t, body, spanDurationMetricName)
	assert.Contains(t, body, "service.name")
}

func TestOTLPMetricsExporterExportProtobufIsDecodable(t *testing.T) {
	srv := newCaptureServer(t)
	exp := makeExporterWithServer(t, srv, "http/protobuf")
	gs := &pb.ClientGroupedStats{
		Service:   "svc",
		Resource:  "web.request",
		OkSummary: encodeSketch(t, 50e6),
	}
	require.NoError(t, exp.export(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs})))

	var decoded otlpcollectormetrics.ExportMetricsServiceRequest
	require.NoError(t, proto.Unmarshal(srv.lastBody, &decoded))
	require.Len(t, decoded.ResourceMetrics, 1)
	require.Len(t, decoded.ResourceMetrics[0].ScopeMetrics, 1)
	assert.Equal(t, spanDurationMetricName, decoded.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name)
}

func TestOTLPMetricsExporterExportHTTPError(t *testing.T) {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(errSrv.Close)
	exp := &otlpMetricsExporter{
		client:   errSrv.Client(),
		url:      errSrv.URL,
		protocol: "http/json",
		cfg:      internalconfig.CreateNew(),
	}
	gs := &pb.ClientGroupedStats{Service: "svc", Resource: "op", OkSummary: encodeSketch(t, 50e6)}
	err := exp.export(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestOTLPMetricsExporterCustomHeaders(t *testing.T) {
	var gotHeader string
	hdrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom-Header")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hdrSrv.Close)
	exp := &otlpMetricsExporter{
		client:   hdrSrv.Client(),
		url:      hdrSrv.URL,
		headers:  map[string]string{"X-Custom-Header": "my-value"},
		protocol: "http/json",
		cfg:      internalconfig.CreateNew(),
	}
	gs := &pb.ClientGroupedStats{Service: "svc", Resource: "op", OkSummary: encodeSketch(t, 50e6)}
	require.NoError(t, exp.export(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs})))
	assert.Equal(t, "my-value", gotHeader)
}

// ---- config integration ----

func TestOTLPMetricsProtocolDefaultIsProtobuf(t *testing.T) {
	cfg := internalconfig.CreateNew()
	assert.Equal(t, "http/protobuf", cfg.OTLPMetricsProtocol())
}
