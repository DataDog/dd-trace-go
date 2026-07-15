// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/otlp/export"
)

type fakeTransport struct {
	mu        sync.Mutex
	requests  []capturedRequest
	responder func(attempt int) (int, string)
}

type capturedRequest struct {
	url     string
	headers http.Header
	body    []byte
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	attempt := len(f.requests)
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	f.requests = append(f.requests, capturedRequest{url: req.URL.String(), headers: req.Header.Clone(), body: body})
	f.mu.Unlock()

	code, respBody := 200, ""
	if f.responder != nil {
		code, respBody = f.responder(attempt)
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(respBody)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func (f *fakeTransport) captured() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func httpClient(f *fakeTransport) *http.Client { return &http.Client{Transport: f} }

func sampleTrace() *tracepb.ExportTraceServiceRequest {
	return &tracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{
			ScopeSpans: []*tracev1.ScopeSpans{{
				Spans: []*tracev1.Span{{
					TraceId: []byte("0123456789abcdef"),
					SpanId:  []byte("01234567"),
					Name:    "op",
				}},
			}},
		}},
	}
}

func TestExportTraces_DatadogRoute(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	req := sampleTrace()
	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{req})
	require.NoError(t, err)
	require.True(t, res.OK())
	require.Len(t, res.Requests, 1)
	assert.Equal(t, 0, res.Requests[0].Index)
	assert.Equal(t, 200, res.Requests[0].StatusCode)
	assert.Equal(t, 1, res.Requests[0].Attempts)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "https://otlp.datadoghq.com/v1/traces", reqs[0].url)
	assert.Equal(t, "key", reqs[0].headers.Get("dd-api-key"))
	assert.Equal(t, "application/x-protobuf", reqs[0].headers.Get("Content-Type"))
	assert.Empty(t, reqs[0].headers.Get("dd-otel-metric-config"))

	// Body round-trips (IDs preserved).
	var got tracepb.ExportTraceServiceRequest
	require.NoError(t, proto.Unmarshal(reqs[0].body, &got))
	assert.True(t, proto.Equal(req, &got))
}

func TestExportMetrics_AddsMetricConfigOnDatadogRoute(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewMetricClient(export.Config{Site: "us5.datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	_, err = c.ExportMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{{}})
	require.NoError(t, err)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "https://otlp.us5.datadoghq.com/v1/metrics", reqs[0].url)
	assert.Equal(t, `{"histograms":{"mode":"distributions"}}`, reqs[0].headers.Get("dd-otel-metric-config"))
	assert.Equal(t, "key", reqs[0].headers.Get("dd-api-key"))
}

func TestExportMetrics_CollectorRouteNoAuthNoMetricConfig(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewMetricClient(export.Config{Endpoint: "http://collector:4318", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	_, err = c.ExportMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{{}})
	require.NoError(t, err)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "http://collector:4318/v1/metrics", reqs[0].url)
	assert.Empty(t, reqs[0].headers.Get("dd-api-key"))            // no Datadog auth on collector route
	assert.Empty(t, reqs[0].headers.Get("dd-otel-metric-config")) // metric config is Datadog-route only
}

func TestExportMetrics_EndpointOverrideWithAPIKeyNoMetricConfig(t *testing.T) {
	fake := &fakeTransport{}
	// Datadog-compatible endpoint override + APIKey: auth is injected, but the
	// dd-otel-metric-config header must not leak onto a non-derived endpoint.
	c, err := export.NewMetricClient(export.Config{Endpoint: "http://collector:4318", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	_, err = c.ExportMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{{}})
	require.NoError(t, err)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "key", reqs[0].headers.Get("dd-api-key"))
	assert.Empty(t, reqs[0].headers.Get("dd-otel-metric-config"))
}

func TestNew_RejectsSchemelessEndpoint(t *testing.T) {
	_, err := export.NewTraceClient(export.Config{Endpoint: "collector:4318"})
	assert.Error(t, err)
}

func TestNew_RejectsNonHTTPScheme(t *testing.T) {
	_, err := export.NewTraceClient(export.Config{Endpoint: "grpc://collector:4317"})
	assert.Error(t, err) // OTLP/gRPC is not supported by the HTTP transport
}

func TestExportTraces_PartialSuccessReportsError(t *testing.T) {
	resp := &tracepb.ExportTraceServiceResponse{
		PartialSuccess: &tracepb.ExportTracePartialSuccess{RejectedSpans: 2, ErrorMessage: "2 spans dropped"},
	}
	b, err := proto.Marshal(resp)
	require.NoError(t, err)
	fake := &fakeTransport{responder: func(int) (int, string) { return 200, string(b) }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err) // partial success surfaces as a failed request
	require.Len(t, res.Requests, 1)
	assert.Equal(t, 200, res.Requests[0].StatusCode)
	require.Error(t, res.Requests[0].Err)
	assert.Contains(t, res.Requests[0].Err.Error(), "partial success")
}

func TestExportLogs_Endpoint(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewLogClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	_, err = c.ExportLogs(context.Background(), []*logspb.ExportLogsServiceRequest{{}})
	require.NoError(t, err)
	assert.Equal(t, "https://otlp.datadoghq.com/v1/logs", fake.captured()[0].url)
}

func TestExportTraces_Non200IsFailure(t *testing.T) {
	// A 202 (or any non-200 2xx) is not the OTLP success contract; report failure.
	fake := &fakeTransport{responder: func(int) (int, string) { return 202, "" }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	require.Error(t, res.Requests[0].Err)
	assert.Equal(t, 202, res.Requests[0].StatusCode)
}

func TestExportTraces_UndecodableBodyIsFailure(t *testing.T) {
	// A 200 whose body is not a decodable OTLP response (e.g. a proxy/login page)
	// must be a failed export, not silently counted as zero rejections.
	fake := &fakeTransport{responder: func(int) (int, string) { return 200, "\x08\xff" }} // malformed protobuf
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	require.Error(t, res.Requests[0].Err)
	assert.Contains(t, res.Requests[0].Err.Error(), "not a valid OTLP response")
}

func TestExportTraces_ForwardCompatibleResponseSucceeds(t *testing.T) {
	// A 200 whose body carries a field unknown to ExportTraceServiceResponse (a
	// forward-compatible extension) must still be treated as success: unknown
	// protobuf fields are ignored, not used to reject the response. Bytes:
	// tag(field 15, varint)=0x78, value=0x01.
	fake := &fakeTransport{responder: func(int) (int, string) { return 200, "\x78\x01" }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.NoError(t, err)
	require.Len(t, res.Requests, 1)
	require.NoError(t, res.Requests[0].Err)
}

func TestExportMetrics_PartialSuccessReportsError(t *testing.T) {
	resp := &metricspb.ExportMetricsServiceResponse{
		PartialSuccess: &metricspb.ExportMetricsPartialSuccess{RejectedDataPoints: 4, ErrorMessage: "4 points dropped"},
	}
	b, err := proto.Marshal(resp)
	require.NoError(t, err)
	fake := &fakeTransport{responder: func(int) (int, string) { return 200, string(b) }}
	c, err := export.NewMetricClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{{}})
	require.Error(t, err) // rejected data points surface as a failed request
	require.Len(t, res.Requests, 1)
	require.Error(t, res.Requests[0].Err)
	assert.Contains(t, res.Requests[0].Err.Error(), "partial success")
}

func TestExportLogs_PartialSuccessReportsError(t *testing.T) {
	resp := &logspb.ExportLogsServiceResponse{
		PartialSuccess: &logspb.ExportLogsPartialSuccess{RejectedLogRecords: 3, ErrorMessage: "3 logs dropped"},
	}
	b, err := proto.Marshal(resp)
	require.NoError(t, err)
	fake := &fakeTransport{responder: func(int) (int, string) { return 200, string(b) }}
	c, err := export.NewLogClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportLogs(context.Background(), []*logspb.ExportLogsServiceRequest{{}})
	require.Error(t, err) // rejected log records surface as a failed request
	require.Len(t, res.Requests, 1)
	require.Error(t, res.Requests[0].Err)
	assert.Contains(t, res.Requests[0].Err.Error(), "partial success")
}

func TestExportTraces_PerRequestRows(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace(), sampleTrace(), sampleTrace()})
	require.NoError(t, err)
	require.Len(t, res.Requests, 3) // one row per request, not flattened spans
	assert.Len(t, fake.captured(), 3)
	for i, rr := range res.Requests {
		assert.Equal(t, i, rr.Index)
	}
}

func TestExportTraces_RetryTransient(t *testing.T) {
	fake := &fakeTransport{responder: func(int) (int, string) { return 503, "unavailable" }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake), MaxAttempts: 3})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	assert.Equal(t, 3, res.Requests[0].Attempts) // total attempts == MaxAttempts
	assert.True(t, res.Requests[0].Retriable)
	assert.Equal(t, 503, res.Requests[0].StatusCode)
}

func TestExportTraces_PermanentError(t *testing.T) {
	fake := &fakeTransport{responder: func(int) (int, string) { return 400, "bad" }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake), MaxAttempts: 3})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	assert.Equal(t, 1, res.Requests[0].Attempts) // not retried
	assert.False(t, res.Requests[0].Retriable)
	assert.Equal(t, 400, res.Requests[0].StatusCode)
}

func TestExportTraces_NonRetryableServerErrorNotRetried(t *testing.T) {
	// 500 is a 5xx but not in the OTLP retryable set (429/502/503/504); it must
	// not burn every attempt like the generic classifier would.
	fake := &fakeTransport{responder: func(int) (int, string) { return 500, "boom" }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake), MaxAttempts: 3})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	assert.Equal(t, 1, res.Requests[0].Attempts) // 500 is permanent under OTLP rules
	assert.False(t, res.Requests[0].Retriable)
	assert.Equal(t, 500, res.Requests[0].StatusCode)
}

func TestExportTraces_RetriesBadGateway(t *testing.T) {
	// 502 is in the OTLP retryable set.
	fake := &fakeTransport{responder: func(int) (int, string) { return 502, "" }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake), MaxAttempts: 2})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	assert.Equal(t, 2, res.Requests[0].Attempts)
	assert.True(t, res.Requests[0].Retriable)
}

func TestExportTraces_SurfacesDecodedStatusMessage(t *testing.T) {
	// OTLP/HTTP error bodies are a google.rpc.Status protobuf; the snippet should
	// show its message, not raw protobuf control bytes.
	var status []byte
	status = protowire.AppendTag(status, 1, protowire.VarintType)
	status = protowire.AppendVarint(status, 3)
	status = protowire.AppendTag(status, 2, protowire.BytesType)
	status = protowire.AppendBytes(status, []byte("resource_spans[0] rejected: bad trace_id"))

	fake := &fakeTransport{responder: func(int) (int, string) { return 400, string(status) }}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	assert.Equal(t, "resource_spans[0] rejected: bad trace_id", res.Requests[0].ResponseSnippet)
}

func TestExportTraces_NilRequest(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)

	res, err := c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{nil})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	assert.Error(t, res.Requests[0].Err)
	assert.Empty(t, fake.captured()) // nil request never sent
}

func TestExportTraces_ContextCancelNotRetriable(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewTraceClient(export.Config{Site: "datadoghq.com", APIKey: "key", HTTPClient: httpClient(fake)})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := c.ExportTraces(ctx, []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	assert.False(t, res.Requests[0].Retriable)
}

func TestNew_RequiresAPIKeyOrEndpoint(t *testing.T) {
	_, err := export.NewTraceClient(export.Config{Site: "datadoghq.com"})
	assert.Error(t, err)
}

func TestNew_EndpointTrimsTrailingSlash(t *testing.T) {
	fake := &fakeTransport{}
	c, err := export.NewTraceClient(export.Config{Endpoint: "http://collector:4318/", HTTPClient: httpClient(fake)})
	require.NoError(t, err)
	_, err = c.ExportTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.NoError(t, err)
	assert.Equal(t, "http://collector:4318/v1/traces", fake.captured()[0].url)
}
