// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ddsketch "github.com/DataDog/sketches-go/ddsketch"
	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

// encodeSketch serializes the given nanosecond values into a DDSketch byte slice.
func encodeSketch(t *testing.T, valuesNs ...float64) []byte {
	t.Helper()
	sk, err := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
	require.NoError(t, err)
	for _, v := range valuesNs {
		require.NoError(t, sk.Add(v))
	}
	var b []byte
	sk.Encode(&b, false)
	return b
}

// makePayload builds a minimal ClientStatsPayload with one stat bucket.
func makePayload(service, env, ver string, groups []*pb.ClientGroupedStats) *pb.ClientStatsPayload {
	startNs := uint64(time.Now().Add(-10 * time.Second).UnixNano())
	durNs := uint64((10 * time.Second).Nanoseconds())
	return &pb.ClientStatsPayload{
		Service: service,
		Env:     env,
		Version: ver,
		Stats: []*pb.ClientStatsBucket{
			{Start: startNs, Duration: durNs, Stats: groups},
		},
	}
}

// kvAttrsToMap converts a []*otlpcommon.KeyValue slice to map[string]string for assertions.
func kvAttrsToMap(kvs []*otlpcommon.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		switch v := kv.Value.Value.(type) {
		case *otlpcommon.AnyValue_StringValue:
			m[kv.Key] = v.StringValue
		case *otlpcommon.AnyValue_BoolValue:
			m[kv.Key] = fmt.Sprintf("%v", v.BoolValue)
		case *otlpcommon.AnyValue_IntValue:
			m[kv.Key] = fmt.Sprintf("%d", v.IntValue)
		case *otlpcommon.AnyValue_DoubleValue:
			m[kv.Key] = fmt.Sprintf("%g", v.DoubleValue)
		}
	}
	return m
}

// scopeAttr returns the string value of a scope attribute by key.
func scopeAttr(sm *otlpmetrics.ScopeMetrics, key string) string {
	return kvAttrsToMap(sm.Scope.Attributes)[key]
}

// ---- sketchToHistogram ----

func TestSketchToHistogramEmpty(t *testing.T) {
	sk, err := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
	require.NoError(t, err)
	var b []byte
	sk.Encode(&b, false)
	_, _, _, _, count, err := sketchToHistogram(b, spanMetricBounds)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count)
}

func TestSketchToHistogramBucketPlacement(t *testing.T) {
	// 5 ms = 0.005 s → between bounds[1]=0.004 and bounds[2]=0.006 → bucket index 2
	b := encodeSketch(t, 5e6) // 5ms in ns
	buckets, sum, minSec, maxSec, count, err := sketchToHistogram(b, spanMetricBounds)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count)
	assert.InEpsilon(t, 0.005, sum, 0.01)
	assert.InEpsilon(t, 0.005, minSec, 0.01)
	assert.InEpsilon(t, 0.005, maxSec, 0.01)
	assert.Equal(t, len(spanMetricBounds)+1, len(buckets))
	assert.Equal(t, uint64(1), buckets[2], "5ms should land in bucket 2")
	for i, c := range buckets {
		if i != 2 {
			assert.Equal(t, uint64(0), c, "bucket %d should be empty", i)
		}
	}
}

func TestSketchToHistogramOverflowBucket(t *testing.T) {
	// 20 s > bounds[15]=15 → last (overflow) bucket
	b := encodeSketch(t, 20e9)
	buckets, _, _, _, count, err := sketchToHistogram(b, spanMetricBounds)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, uint64(1), buckets[len(spanMetricBounds)])
}

func TestSketchToHistogramSortSearchSemantics(t *testing.T) {
	// Verify that sort.Search(bounds, func(i) bounds[i] > v) puts a value clearly inside
	// a bucket into the correct index. 50ms = 0.05 s is bounds[5]; values slightly above
	// it should land in bucket 6. Use 60ms (0.06 s) which is unambiguously in [0.05, 0.1).
	b := encodeSketch(t, 60e6) // 60ms in ns
	buckets, _, _, _, count, err := sketchToHistogram(b, spanMetricBounds)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count)
	// bounds[5]=0.05, bounds[6]=0.1 → 0.06 s belongs in bucket 6
	assert.Equal(t, uint64(1), buckets[6], "60ms should land in bucket 6")
}

func TestSketchToHistogramMultipleValues(t *testing.T) {
	// 1ms + 500ms + 3s → three separate buckets, sum ≈ 3.501 s
	b := encodeSketch(t, 1e6, 500e6, 3e9)
	buckets, sum, _, _, count, err := sketchToHistogram(b, spanMetricBounds)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), count)
	assert.InDelta(t, 3.501, sum, 0.05)
	nonZero := 0
	for _, c := range buckets {
		if c > 0 {
			nonZero++
		}
	}
	assert.Equal(t, 3, nonZero, "three distinct buckets should be occupied")
}

// ---- BuildOTLPMetricsRequest ----

func TestBuildOTLPMetricsRequestNilOnEmpty(t *testing.T) {
	cfg := internalconfig.CreateNew()
	payload := makePayload("svc", "", "", nil)
	assert.Nil(t, BuildOTLPMetricsRequest(payload, cfg))
}

func TestBuildOTLPMetricsRequestStructure(t *testing.T) {
	cfg := internalconfig.CreateNew()
	gs := &pb.ClientGroupedStats{
		Service:      "svc",
		Name:         "web.request",
		Resource:     "/users",
		Type:         "web",
		SpanKind:     "server",
		TopLevelHits: 1,
		OkSummary:    encodeSketch(t, 50e6), // 50ms
	}
	req := BuildOTLPMetricsRequest(makePayload("svc", "prod", "1.0", []*pb.ClientGroupedStats{gs}), cfg)
	require.NotNil(t, req)

	rm := req.ResourceMetrics
	require.Len(t, rm, 1)
	sm := rm[0].ScopeMetrics
	require.Len(t, sm, 1)
	assert.Equal(t, "dd-trace-go", sm[0].Scope.Name)

	m := sm[0].Metrics
	require.Len(t, m, 1)
	assert.Equal(t, spanDurationMetricName, m[0].Name)
	assert.Equal(t, "s", m[0].Unit)

	hist := m[0].GetHistogram()
	require.NotNil(t, hist)
	assert.Equal(t, otlpmetrics.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, hist.AggregationTemporality)

	require.Len(t, hist.DataPoints, 1)
	dp := hist.DataPoints[0]
	assert.Equal(t, spanMetricBounds, dp.ExplicitBounds)
	assert.Equal(t, len(spanMetricBounds)+1, len(dp.BucketCounts))
	require.NotNil(t, dp.Sum)
	assert.Equal(t, uint64(1), dp.Count)
}

func TestBuildOTLPMetricsRequestOkAndErrorDataPoints(t *testing.T) {
	cfg := internalconfig.CreateNew()
	gs := &pb.ClientGroupedStats{
		Service:      "svc",
		Resource:     "/users",
		Errors:       1,
		OkSummary:    encodeSketch(t, 50e6),
		ErrorSummary: encodeSketch(t, 100e6),
	}
	req := BuildOTLPMetricsRequest(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}), cfg)
	require.NotNil(t, req)
	hist := req.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetHistogram()
	require.Len(t, hist.DataPoints, 2)
}

func TestBuildOTLPMetricsRequestMultipleServices(t *testing.T) {
	cfg := internalconfig.CreateNew()
	gs1 := &pb.ClientGroupedStats{Service: "svc-a", Resource: "res-a", OkSummary: encodeSketch(t, 50e6)}
	gs2 := &pb.ClientGroupedStats{Service: "svc-b", Resource: "res-b", OkSummary: encodeSketch(t, 100e6)}
	req := BuildOTLPMetricsRequest(makePayload("svc-a", "", "", []*pb.ClientGroupedStats{gs1, gs2}), cfg)
	require.NotNil(t, req)
	sm := req.ResourceMetrics[0].ScopeMetrics
	require.Len(t, sm, 2)
	// Scopes must be emitted in sorted service-name order.
	assert.Equal(t, "svc-a", scopeAttr(sm[0], "service.name"))
	assert.Equal(t, "svc-b", scopeAttr(sm[1], "service.name"))
}

// ---- Resource attributes ----

func TestBuildMetricsResourceSDKAttributes(t *testing.T) {
	cfg := internalconfig.CreateNew()
	res := buildMetricsResource(cfg, makePayload("svc", "", "", nil), false)
	m := kvAttrsToMap(res.Attributes)
	assert.Equal(t, "datadog", m["telemetry.sdk.name"])
	assert.Equal(t, "go", m["telemetry.sdk.language"])
	assert.NotEmpty(t, m["telemetry.sdk.version"])
	assert.NotContains(t, m, "service.name")
}

func TestBuildMetricsResourceHostnameOmitted(t *testing.T) {
	cfg := internalconfig.CreateNew()
	// DD_TRACE_REPORT_HOSTNAME is unset → ReportHostname() returns false.
	res := buildMetricsResource(cfg, makePayload("svc", "", "", nil), false)
	assert.NotContains(t, kvAttrsToMap(res.Attributes), "host.name")
}

func TestBuildMetricsResourceProcessTagsDefaultMode(t *testing.T) {
	cfg := internalconfig.CreateNew()
	payload := makePayload("svc", "", "", nil)
	payload.ProcessTags = "entrypoint.name:myapp,entrypoint.type:binary"
	res := buildMetricsResource(cfg, payload, false /* default mode */)
	m := kvAttrsToMap(res.Attributes)
	assert.Equal(t, "myapp", m["dd.entrypoint.name"])
	assert.Equal(t, "binary", m["dd.entrypoint.type"])
}

func TestBuildMetricsResourceNoProcessTagsInOtelMode(t *testing.T) {
	cfg := internalconfig.CreateNew()
	payload := makePayload("svc", "", "", nil)
	payload.ProcessTags = "entrypoint.name:myapp"
	res := buildMetricsResource(cfg, payload, true /* otelMode */)
	assert.NotContains(t, kvAttrsToMap(res.Attributes), "dd.entrypoint.name")
}

// ---- Scope attributes ----

func TestBuildScopeAttributesAll(t *testing.T) {
	m := kvAttrsToMap(buildScopeAttributes("my-svc", "prod", "2.1.0"))
	assert.Equal(t, "my-svc", m["service.name"])
	assert.Equal(t, "prod", m["deployment.environment.name"])
	assert.Equal(t, "2.1.0", m["service.version"])
}

func TestBuildScopeAttributesOmitsEmpty(t *testing.T) {
	m := kvAttrsToMap(buildScopeAttributes("svc", "", ""))
	assert.Equal(t, "svc", m["service.name"])
	assert.NotContains(t, m, "deployment.environment.name")
	assert.NotContains(t, m, "service.version")
}

// ---- Data-point attributes ----

func TestDataPointAttributesOTelMode(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Name:           "web.request",
		Resource:       "/users",
		Type:           "web",
		SpanKind:       "server",
		HTTPMethod:     "GET",
		HTTPStatusCode: 200,
		TopLevelHits:   1,
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true /* otelMode */))
	assert.Equal(t, "/users", m["span.name"])
	assert.Equal(t, "server", m["span.kind"])
	assert.Equal(t, "GET", m["http.request.method"])
	assert.Equal(t, "200", m["http.response.status_code"])
	assert.NotContains(t, m, "dd.operation.name")
	assert.NotContains(t, m, "dd.span.type")
	assert.NotContains(t, m, "dd.span.top_level")
}

func TestDataPointAttributesDefaultMode(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Name:         "web.request",
		Resource:     "/users",
		Type:         "web",
		TopLevelHits: 1,
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, false /* default mode */))
	assert.Equal(t, "web.request", m["dd.operation.name"])
	assert.Equal(t, "web", m["dd.span.type"])
	assert.Equal(t, "true", m["dd.span.top_level"])
}

func TestDataPointAttributesTopLevelFalse(t *testing.T) {
	gs := &pb.ClientGroupedStats{Resource: "child-resource", TopLevelHits: 0}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, false))
	assert.Equal(t, "false", m["dd.span.top_level"])
}

func TestDataPointAttributesErrorStatusCode(t *testing.T) {
	gs := &pb.ClientGroupedStats{Resource: "/err"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, true /* isError */, true))
	assert.Equal(t, "ERROR", m["status.code"])
}

func TestDataPointAttributesPeerTags(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Resource: "/users/{id}",
		PeerTags: []string{"http.route:/users/{id}", "peer.service:db"},
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
	assert.Equal(t, "/users/{id}", m["http.route"])
	assert.Equal(t, "db", m["peer.service"])
}
