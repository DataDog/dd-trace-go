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
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/otlp/export"
)

const testAPIKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

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

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	attempt := len(f.requests)
	body, _ := io.ReadAll(request.Body)
	_ = request.Body.Close()
	f.requests = append(f.requests, capturedRequest{url: request.URL.String(), headers: request.Header.Clone(), body: body})
	f.mu.Unlock()

	status, responseBody := http.StatusOK, ""
	if f.responder != nil {
		status, responseBody = f.responder(attempt)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Header:     http.Header{"Content-Type": []string{internalconfig.OTLPContentTypeHeader}},
	}, nil
}

func (f *fakeTransport) captured() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedRequest(nil), f.requests...)
}

func newDatadogClient(t *testing.T, fake *fakeTransport, opts ...export.ClientOption) *export.Client {
	t.Helper()
	options := append([]export.ClientOption{
		export.WithDatadogIntake("datadoghq.com", testAPIKey),
		export.WithHTTPClient(&http.Client{Transport: fake}),
	}, opts...)
	client, err := export.NewClient(options...)
	require.NoError(t, err)
	return client
}

func newCollectorClient(t *testing.T, fake *fakeTransport, endpoint string, opts ...export.ClientOption) *export.Client {
	t.Helper()
	options := append([]export.ClientOption{
		export.WithCollectorEndpoint(endpoint),
		export.WithHTTPClient(&http.Client{Transport: fake}),
	}, opts...)
	client, err := export.NewClient(options...)
	require.NoError(t, err)
	return client
}

func useFreshGlobalConfig(t *testing.T) {
	t.Helper()
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() {
		internalconfig.SetUseFreshConfig(false)
		internalconfig.CreateNew()
	})
}

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

func sampleMetric() *metricspb.ExportMetricsServiceRequest {
	return &metricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			ScopeMetrics: []*metricsv1.ScopeMetrics{{
				Metrics: []*metricsv1.Metric{{
					Name: "requests",
					Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
						DataPoints: []*metricsv1.NumberDataPoint{{Value: &metricsv1.NumberDataPoint_AsInt{AsInt: 1}}},
					}},
				}},
			}},
		}},
	}
}

func sampleLogs() *logspb.ExportLogsServiceRequest {
	return &logspb.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "completed"}},
				}},
			}},
		}},
	}
}

func TestSubmitTraces_DatadogRoute(t *testing.T) {
	fake := &fakeTransport{}
	client := newDatadogClient(t, fake)
	request := sampleTrace()

	result, err := client.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{request})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Sent)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Requests, 1)
	assert.Equal(t, 0, result.Requests[0].Index)
	assert.Equal(t, http.StatusOK, result.Requests[0].StatusCode)
	assert.Equal(t, 1, result.Requests[0].Attempts)

	requests := fake.captured()
	require.Len(t, requests, 1)
	assert.Equal(t, "https://otlp.datadoghq.com/v1/traces", requests[0].url)
	assert.Equal(t, testAPIKey, requests[0].headers.Get("dd-api-key"))
	assert.Equal(t, internalconfig.OTLPContentTypeHeader, requests[0].headers.Get("Content-Type"))
	assert.Empty(t, requests[0].headers.Get("dd-otel-metric-config"))

	var got tracepb.ExportTraceServiceRequest
	require.NoError(t, proto.Unmarshal(requests[0].body, &got))
	assert.True(t, proto.Equal(request, &got))
}

func TestSubmitTraces_RequestSizeLimit(t *testing.T) {
	request := sampleTrace()
	size := proto.Size(request)
	require.Positive(t, size)

	t.Run("at limit", func(t *testing.T) {
		fake := &fakeTransport{}
		result, err := newDatadogClient(t, fake, export.WithMaxRequestSize(size)).SubmitTraces(
			context.Background(),
			[]*tracepb.ExportTraceServiceRequest{request},
		)
		require.NoError(t, err)
		assert.Equal(t, 1, result.Sent)
		assert.Len(t, fake.captured(), 1)
	})

	t.Run("over limit", func(t *testing.T) {
		fake := &fakeTransport{}
		result, err := newDatadogClient(t, fake, export.WithMaxRequestSize(size-1)).SubmitTraces(
			context.Background(),
			[]*tracepb.ExportTraceServiceRequest{request},
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, export.ErrRequestTooLarge)
		assert.Equal(t, 1, result.Failed)
		require.Len(t, result.Requests, 1)
		assert.Zero(t, result.Requests[0].Attempts)
		assert.Zero(t, result.Requests[0].StatusCode)
		assert.False(t, result.Requests[0].Retriable)
		assert.Empty(t, fake.captured())
	})

	t.Run("disabled", func(t *testing.T) {
		fake := &fakeTransport{}
		result, err := newDatadogClient(t, fake, export.WithMaxRequestSize(0)).SubmitTraces(
			context.Background(),
			[]*tracepb.ExportTraceServiceRequest{request},
		)
		require.NoError(t, err)
		assert.Equal(t, 1, result.Sent)
		assert.Len(t, fake.captured(), 1)
	})
}

func TestSubmitTraces_MarshalFailure(t *testing.T) {
	request := sampleTrace()
	request.ResourceSpans[0].ScopeSpans[0].Spans[0].Name = string([]byte{0xff})
	fake := &fakeTransport{}

	result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{request})
	require.Error(t, err)
	require.Len(t, result.Requests, 1)
	assert.Contains(t, result.Requests[0].Err.Error(), "marshal")
	assert.Zero(t, result.Requests[0].Attempts)
	assert.Empty(t, fake.captured())
}

func TestSubmitMetrics_DatadogRoute(t *testing.T) {
	fake := &fakeTransport{}
	client, err := export.NewClient(
		export.WithDatadogIntake("us5.datadoghq.com", testAPIKey),
		export.WithHTTPClient(&http.Client{Transport: fake}),
		export.WithHeaders(map[string]string{
			"Content-Type":          "text/plain",
			"dd-api-key":            "wrong-key",
			"dd-otel-metric-config": "wrong-config",
		}),
	)
	require.NoError(t, err)

	metric := sampleMetric()
	_, err = client.SubmitMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{metric})
	require.NoError(t, err)

	request := fake.captured()[0]
	assert.Equal(t, "https://otlp.us5.datadoghq.com/v1/metrics", request.url)
	assert.Equal(t, testAPIKey, request.headers.Get("dd-api-key"))
	assert.Equal(t, `{"histograms":{"mode":"distributions"}}`, request.headers.Get("dd-otel-metric-config"))
	var got metricspb.ExportMetricsServiceRequest
	require.NoError(t, proto.Unmarshal(request.body, &got))
	assert.True(t, proto.Equal(metric, &got))
}

func TestSubmitMetrics_CollectorRoute(t *testing.T) {
	fake := &fakeTransport{}
	client := newCollectorClient(t, fake, "http://collector:4318/prefix/",
		export.WithHeaders(map[string]string{
			"Authorization": "Bearer token",
			"Content-Type":  "text/plain",
		}),
	)

	_, err := client.SubmitMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{sampleMetric()})
	require.NoError(t, err)

	request := fake.captured()[0]
	assert.Equal(t, "http://collector:4318/prefix/v1/metrics", request.url)
	assert.Equal(t, "Bearer token", request.headers.Get("Authorization"))
	assert.Equal(t, internalconfig.OTLPContentTypeHeader, request.headers.Get("Content-Type"))
	assert.Empty(t, request.headers.Get("dd-api-key"))
	assert.Empty(t, request.headers.Get("dd-otel-metric-config"))
}

func TestSubmitLogs_DatadogRoute(t *testing.T) {
	fake := &fakeTransport{}
	client := newDatadogClient(t, fake)
	logs := sampleLogs()

	result, err := client.SubmitLogs(context.Background(), []*logspb.ExportLogsServiceRequest{logs})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Sent)
	request := fake.captured()[0]
	assert.Equal(t, "https://otlp.datadoghq.com/v1/logs", request.url)
	var got logspb.ExportLogsServiceRequest
	require.NoError(t, proto.Unmarshal(request.body, &got))
	assert.True(t, proto.Equal(logs, &got))
}

func TestNewClient_RoutingValidation(t *testing.T) {
	tests := []struct {
		name string
		opts []export.ClientOption
	}{
		{name: "route required"},
		{name: "routes conflict", opts: []export.ClientOption{export.WithDatadogIntake("datadoghq.com", testAPIKey), export.WithCollectorEndpoint("http://collector:4318")}},
		{name: "invalid API key", opts: []export.ClientOption{export.WithDatadogIntake("datadoghq.com", "key")}},
		{name: "schemeless endpoint", opts: []export.ClientOption{export.WithCollectorEndpoint("collector:4318")}},
		{name: "gRPC endpoint", opts: []export.ClientOption{export.WithCollectorEndpoint("grpc://collector:4317")}},
		{name: "endpoint query", opts: []export.ClientOption{export.WithCollectorEndpoint("http://collector:4318?token=x")}},
		{name: "endpoint user info", opts: []export.ClientOption{export.WithCollectorEndpoint("http://user:pass@collector:4318")}},
		{name: "site with path", opts: []export.ClientOption{export.WithDatadogIntake("datadoghq.com/path", testAPIKey)}},
		{name: "zero attempts", opts: []export.ClientOption{export.WithCollectorEndpoint("http://collector:4318"), export.WithMaxAttempts(0)}},
		{name: "negative timeout", opts: []export.ClientOption{export.WithCollectorEndpoint("http://collector:4318"), export.WithRequestTimeout(-1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := export.NewClient(test.opts...)
			assert.Error(t, err)
		})
	}
}

func TestNewClient_InheritsGlobalDatadogConfig(t *testing.T) {
	useFreshGlobalConfig(t)
	t.Setenv("DD_SITE", "datadoghq.eu")
	t.Setenv("DD_API_KEY", testAPIKey)
	fake := &fakeTransport{}

	client, err := export.NewClient(
		export.WithDatadogIntake("", ""),
		export.WithHTTPClient(&http.Client{Transport: fake}),
	)
	require.NoError(t, err)
	_, err = client.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.NoError(t, err)

	request := fake.captured()[0]
	assert.Equal(t, "https://otlp.datadoghq.eu/v1/traces", request.url)
	assert.Equal(t, testAPIKey, request.headers.Get("dd-api-key"))
}

func TestSubmit_PartialSuccess(t *testing.T) {
	t.Run("traces", func(t *testing.T) {
		body, err := proto.Marshal(&tracepb.ExportTraceServiceResponse{PartialSuccess: &tracepb.ExportTracePartialSuccess{RejectedSpans: 2, ErrorMessage: "dropped"}})
		require.NoError(t, err)
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, string(body) }}
		result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
		require.Error(t, err)
		assert.Equal(t, 0, result.Sent)
		assert.Equal(t, 1, result.Failed)
		assert.Equal(t, int64(2), result.Requests[0].RejectedItems)
		assert.Len(t, fake.captured(), 1)
	})

	t.Run("metrics", func(t *testing.T) {
		body, err := proto.Marshal(&metricspb.ExportMetricsServiceResponse{PartialSuccess: &metricspb.ExportMetricsPartialSuccess{RejectedDataPoints: 4, ErrorMessage: "dropped"}})
		require.NoError(t, err)
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, string(body) }}
		result, err := newDatadogClient(t, fake).SubmitMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{{}})
		require.Error(t, err)
		assert.Equal(t, int64(4), result.Requests[0].RejectedItems)
	})

	t.Run("logs", func(t *testing.T) {
		body, err := proto.Marshal(&logspb.ExportLogsServiceResponse{PartialSuccess: &logspb.ExportLogsPartialSuccess{RejectedLogRecords: 3, ErrorMessage: "dropped"}})
		require.NoError(t, err)
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, string(body) }}
		result, err := newDatadogClient(t, fake).SubmitLogs(context.Background(), []*logspb.ExportLogsServiceRequest{{}})
		require.Error(t, err)
		assert.Equal(t, int64(3), result.Requests[0].RejectedItems)
	})

	t.Run("warning", func(t *testing.T) {
		body, err := proto.Marshal(&tracepb.ExportTraceServiceResponse{PartialSuccess: &tracepb.ExportTracePartialSuccess{ErrorMessage: "collector warning"}})
		require.NoError(t, err)
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, string(body) }}
		result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Sent)
		assert.Equal(t, "collector warning", result.Requests[0].ResponseSnippet)
		assert.Equal(t, 1, result.Requests[0].Attempts)
		assert.Len(t, fake.captured(), 1)
	})

	t.Run("negative count", func(t *testing.T) {
		body, err := proto.Marshal(&tracepb.ExportTraceServiceResponse{PartialSuccess: &tracepb.ExportTracePartialSuccess{RejectedSpans: -1}})
		require.NoError(t, err)
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, string(body) }}
		result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
		require.Error(t, err)
		assert.Equal(t, 1, result.Failed)
		assert.Contains(t, result.Requests[0].Err.Error(), "negative rejected-item count")
		assert.Len(t, fake.captured(), 1)
	})
}

func TestSubmitTraces_ResponseValidation(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusAccepted, "" }}
		result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
		require.Error(t, err)
		assert.Equal(t, http.StatusAccepted, result.Requests[0].StatusCode)
	})

	t.Run("malformed body", func(t *testing.T) {
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, "\x08\xff" }}
		result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
		require.Error(t, err)
		assert.Contains(t, result.Requests[0].Err.Error(), "not a valid OTLP response")
		assert.Len(t, fake.captured(), 1)
	})

	t.Run("unknown response field", func(t *testing.T) {
		response := protowire.AppendTag(nil, 15, protowire.VarintType)
		response = protowire.AppendVarint(response, 1)
		fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusOK, string(response) }}
		result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Sent)
		assert.Empty(t, result.Requests[0].ResponseSnippet)
	})
}

func TestSubmitTraces_ResultsFollowInputOrder(t *testing.T) {
	fake := &fakeTransport{}
	client := newDatadogClient(t, fake)
	result, err := client.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace(), nil, sampleTrace()})
	require.Error(t, err)
	require.Len(t, result.Requests, 3)
	assert.Equal(t, 2, result.Sent)
	assert.Equal(t, 1, result.Failed)
	assert.Len(t, fake.captured(), 2)
	for index, request := range result.Requests {
		assert.Equal(t, index, request.Index)
	}
	assert.Error(t, result.Requests[1].Err)
}

func TestSubmitTraces_RetryClassification(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantAttempts int
		wantRetry    bool
	}{
		{name: "service unavailable", status: http.StatusServiceUnavailable, wantAttempts: 3, wantRetry: true},
		{name: "bad gateway", status: http.StatusBadGateway, wantAttempts: 3, wantRetry: true},
		{name: "internal server error", status: http.StatusInternalServerError, wantAttempts: 1, wantRetry: false},
		{name: "bad request", status: http.StatusBadRequest, wantAttempts: 1, wantRetry: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTransport{responder: func(int) (int, string) { return test.status, "failed" }}
			client := newDatadogClient(t, fake, export.WithMaxAttempts(3))
			result, err := client.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
			require.Error(t, err)
			assert.Equal(t, test.wantAttempts, result.Requests[0].Attempts)
			assert.Equal(t, test.wantRetry, result.Requests[0].Retriable)
		})
	}
}

func TestSubmitTraces_RetrySucceeds(t *testing.T) {
	fake := &fakeTransport{responder: func(attempt int) (int, string) {
		if attempt == 0 {
			return http.StatusServiceUnavailable, "busy"
		}
		return http.StatusOK, ""
	}}

	result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Sent)
	assert.Equal(t, 2, result.Requests[0].Attempts)
	assert.False(t, result.Requests[0].Retriable)
	requests := fake.captured()
	require.Len(t, requests, 2)
	assert.Equal(t, requests[0].body, requests[1].body)
}

func TestSubmitTraces_SurfacesStatusMessage(t *testing.T) {
	status, marshalErr := proto.Marshal(&statuspb.Status{Message: "resource_spans[0] rejected: bad trace_id"})
	require.NoError(t, marshalErr)
	fake := &fakeTransport{responder: func(int) (int, string) { return http.StatusBadRequest, string(status) }}

	result, err := newDatadogClient(t, fake).SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.Error(t, err)
	assert.Equal(t, "resource_spans[0] rejected: bad trace_id", result.Requests[0].ResponseSnippet)
}

func TestSubmitTraces_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := newDatadogClient(t, &fakeTransport{}).SubmitTraces(ctx, []*tracepb.ExportTraceServiceRequest{sampleTrace(), sampleTrace()})
	require.Error(t, err)
	assert.Equal(t, 0, result.Sent)
	assert.Equal(t, 2, result.Failed)
	assert.Equal(t, 0, result.Requests[0].Attempts)
	assert.True(t, result.Requests[0].Retriable)
}

func TestSubmitTraces_CancelAfterLastRequestIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeTransport{responder: func(int) (int, string) {
		cancel()
		return http.StatusOK, ""
	}}

	result, err := newDatadogClient(t, fake).SubmitTraces(ctx, []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Sent)
}

func TestClientsKeepDestinationsIsolated(t *testing.T) {
	firstTransport, secondTransport := &fakeTransport{}, &fakeTransport{}
	first := newCollectorClient(t, firstTransport, "http://first:4318", export.WithHeaders(map[string]string{"Authorization": "Bearer first"}))
	second := newCollectorClient(t, secondTransport, "http://second:4318", export.WithHeaders(map[string]string{"Authorization": "Bearer second"}))

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = first.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	}()
	go func() {
		defer wait.Done()
		_, _ = second.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	}()
	wait.Wait()

	assert.Equal(t, "http://first:4318/v1/traces", firstTransport.captured()[0].url)
	assert.Equal(t, "Bearer first", firstTransport.captured()[0].headers.Get("Authorization"))
	assert.Equal(t, "http://second:4318/v1/traces", secondTransport.captured()[0].url)
	assert.Equal(t, "Bearer second", secondTransport.captured()[0].headers.Get("Authorization"))
}

func TestClientConcurrentUse(t *testing.T) {
	fake := &fakeTransport{}
	client := newCollectorClient(t, fake, "http://collector:4318")

	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		_, _ = client.SubmitTraces(context.Background(), []*tracepb.ExportTraceServiceRequest{sampleTrace()})
	}()
	go func() {
		defer wait.Done()
		_, _ = client.SubmitMetrics(context.Background(), []*metricspb.ExportMetricsServiceRequest{sampleMetric()})
	}()
	go func() {
		defer wait.Done()
		_, _ = client.SubmitLogs(context.Background(), []*logspb.ExportLogsServiceRequest{sampleLogs()})
	}()
	wait.Wait()

	assert.Len(t, fake.captured(), 3)
}
