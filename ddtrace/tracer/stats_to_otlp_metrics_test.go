// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	ddsketch "github.com/DataDog/sketches-go/ddsketch"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

// encodeSketch serializes the given nanosecond values into proto-encoded DDSketch bytes,
// matching the format produced by the stats concentrator (proto.Marshal(sketch.ToProto())).
func encodeSketch(t *testing.T, valuesNs ...float64) []byte {
	t.Helper()
	sk, err := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
	require.NoError(t, err)
	for _, v := range valuesNs {
		require.NoError(t, sk.Add(v))
	}
	b, err := proto.Marshal(sk.ToProto())
	require.NoError(t, err)
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
			m[kv.Key] = strconv.FormatBool(v.BoolValue)
		case *otlpcommon.AnyValue_IntValue:
			m[kv.Key] = strconv.FormatInt(v.IntValue, 10)
		case *otlpcommon.AnyValue_DoubleValue:
			m[kv.Key] = strconv.FormatFloat(v.DoubleValue, 'g', -1, 64)
		}
	}
	return m
}

// kvArrayValue returns the stringValue elements of the named arrayValue attribute, or nil if absent/not an array.
func kvArrayValue(kvs []*otlpcommon.KeyValue, key string) []string {
	for _, kv := range kvs {
		if kv.Key != key {
			continue
		}
		arr, ok := kv.Value.Value.(*otlpcommon.AnyValue_ArrayValue)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(arr.ArrayValue.Values))
		for _, v := range arr.ArrayValue.Values {
			if sv, ok := v.Value.(*otlpcommon.AnyValue_StringValue); ok {
				out = append(out, sv.StringValue)
			}
		}
		return out
	}
	return nil
}

// ---- sketchToHistogram ----

func TestSketchToHistogramEmpty(t *testing.T) {
	sk, err := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
	require.NoError(t, err)
	b, err := proto.Marshal(sk.ToProto())
	require.NoError(t, err)
	_, _, _, _, count, err := sketchToHistogram(b, spanMetricBounds[:])
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count)
}

func TestSketchToHistogramBucketPlacement(t *testing.T) {
	// 5 ms = 0.005 s → between bounds[1]=0.004 and bounds[2]=0.006 → bucket index 2
	b := encodeSketch(t, 5e6) // 5ms in ns
	buckets, sum, minSec, maxSec, count, err := sketchToHistogram(b, spanMetricBounds[:])
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
	buckets, _, _, _, count, err := sketchToHistogram(b, spanMetricBounds[:])
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, uint64(1), buckets[len(spanMetricBounds)])
}

func TestSketchToHistogramMultipleValues(t *testing.T) {
	// 1ms + 500ms + 3s → three separate buckets, sum ≈ 3.501 s
	b := encodeSketch(t, 1e6, 500e6, 3e9)
	buckets, sum, _, _, count, err := sketchToHistogram(b, spanMetricBounds[:])
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

// ---- buildOTLPMetricsRequest (package-internal) ----

func TestBuildOTLPMetricsRequestNilOnEmpty(t *testing.T) {
	cfg := internalconfig.CreateNew()
	payload := makePayload("svc", "", "", nil)
	assert.Nil(t, buildOTLPMetricsRequest(payload, cfg))
}

func TestBuildOTLPMetricsRequestStructure(t *testing.T) {
	cfg := internalconfig.CreateNew()
	gs := &pb.ClientGroupedStats{
		Service:      "svc",
		Name:         "web.request",
		Resource:     "/users",
		Type:         "web",
		SpanKind:     "server",
		Hits:         1,
		TopLevelHits: 1,
		OkSummary:    encodeSketch(t, 50e6), // 50ms
	}
	rm := buildOTLPMetricsRequest(makePayload("svc", "prod", "1.0", []*pb.ClientGroupedStats{gs}), cfg)
	require.NotNil(t, rm)
	require.Len(t, rm, 1)

	sm := rm[0].ScopeMetrics
	require.Len(t, sm, 1)
	assert.Nil(t, sm[0].Scope, "no InstrumentationScope; it would be redundant with telemetry.sdk.* resource attributes")

	m := sm[0].Metrics
	require.Len(t, m, 1)
	assert.Equal(t, spanDurationMetricName, m[0].Name)
	assert.Equal(t, "s", m[0].Unit)

	hist := m[0].GetHistogram()
	require.NotNil(t, hist)
	assert.Equal(t, otlpmetrics.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, hist.AggregationTemporality)

	require.Len(t, hist.DataPoints, 1)
	dp := hist.DataPoints[0]
	assert.Equal(t, spanMetricBounds[:], dp.ExplicitBounds)
	assert.Equal(t, len(spanMetricBounds)+1, len(dp.BucketCounts))
	require.NotNil(t, dp.Sum)
	assert.Equal(t, uint64(1), dp.Count)
}

func TestBuildOTLPMetricsRequestOkAndErrorDataPoints(t *testing.T) {
	cfg := internalconfig.CreateNew()
	gs := &pb.ClientGroupedStats{
		Service:      "svc",
		Resource:     "/users",
		Hits:         2,
		Errors:       1,
		OkSummary:    encodeSketch(t, 50e6),
		ErrorSummary: encodeSketch(t, 100e6),
	}
	rm := buildOTLPMetricsRequest(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}), cfg)
	require.NotNil(t, rm)
	hist := rm[0].ScopeMetrics[0].Metrics[0].GetHistogram()
	require.Len(t, hist.DataPoints, 2)
}

func TestBuildOTLPMetricsRequestMultipleServices(t *testing.T) {
	// Multiple services in one payload share a single scope; every data point carries
	// its own service.name attribute, regardless of whether it matches the payload's service.
	cfg := internalconfig.CreateNew()
	gs1 := &pb.ClientGroupedStats{Service: "svc-a", Resource: "res-a", OkSummary: encodeSketch(t, 50e6)}
	gs2 := &pb.ClientGroupedStats{Service: "svc-b", Resource: "res-b", OkSummary: encodeSketch(t, 100e6)}
	rm := buildOTLPMetricsRequest(makePayload("svc-a", "", "", []*pb.ClientGroupedStats{gs1, gs2}), cfg)
	require.NotNil(t, rm)

	sm := rm[0].ScopeMetrics
	require.Len(t, sm, 1, "single scope regardless of service count")

	dataPoints := sm[0].Metrics[0].GetHistogram().DataPoints
	require.Len(t, dataPoints, 2)

	var svcAPoint, svcBPoint *otlpmetrics.HistogramDataPoint
	for _, dp := range dataPoints {
		m := kvAttrsToMap(dp.Attributes)
		if m["span.name"] == "res-a" {
			svcAPoint = dp
		} else {
			svcBPoint = dp
		}
	}
	require.NotNil(t, svcAPoint)
	require.NotNil(t, svcBPoint)
	assert.Equal(t, "svc-a", kvAttrsToMap(svcAPoint.Attributes)["service.name"])
	assert.Equal(t, "svc-b", kvAttrsToMap(svcBPoint.Attributes)["service.name"])
}

// ---- Resource attributes ----

func TestBuildMetricsResourceSDKAttributes(t *testing.T) {
	res := buildMetricsResource(makePayload("svc", "", "", nil), false, false, "")
	m := kvAttrsToMap(res.Attributes)
	assert.Equal(t, "datadog", m["telemetry.sdk.name"])
	assert.Equal(t, "go", m["telemetry.sdk.language"])
	assert.NotEmpty(t, m["telemetry.sdk.version"])
}

func TestBuildMetricsResourceServiceIdentity(t *testing.T) {
	res := buildMetricsResource(makePayload("my-svc", "prod", "2.1.0", nil), false, false, "")
	m := kvAttrsToMap(res.Attributes)
	assert.Equal(t, "my-svc", m["service.name"])
	assert.Equal(t, "prod", m["deployment.environment.name"])
	assert.Equal(t, "2.1.0", m["service.version"])
}

func TestBuildMetricsResourceServiceIdentityOmitsEmptyEnvVer(t *testing.T) {
	res := buildMetricsResource(makePayload("svc", "", "", nil), false, false, "")
	m := kvAttrsToMap(res.Attributes)
	assert.Equal(t, "svc", m["service.name"])
	assert.NotContains(t, m, "deployment.environment.name")
	assert.NotContains(t, m, "service.version")
}

func TestBuildMetricsResourceHostnameOmitted(t *testing.T) {
	res := buildMetricsResource(makePayload("svc", "", "", nil), false, false, "")
	assert.NotContains(t, kvAttrsToMap(res.Attributes), "host.name")
}

func TestBuildMetricsResourceProcessTagsDefaultMode(t *testing.T) {
	payload := makePayload("svc", "", "", nil)
	payload.ProcessTags = "entrypoint.name:myapp,entrypoint.type:binary"
	res := buildMetricsResource(payload, false /* otelMode */, false, "")
	values := kvArrayValue(res.Attributes, "datadog.process_tags")
	assert.ElementsMatch(t, []string{"entrypoint.name:myapp", "entrypoint.type:binary"}, values)
}

func TestBuildMetricsResourceRuntimeIDDefaultMode(t *testing.T) {
	payload := makePayload("svc", "", "", nil)
	payload.RuntimeID = "abc-123"
	res := buildMetricsResource(payload, false /* otelMode */, false, "")
	assert.Equal(t, "abc-123", kvAttrsToMap(res.Attributes)["datadog.runtime_id"])
}

func TestBuildMetricsResourceNoRuntimeIDWhenEmpty(t *testing.T) {
	res := buildMetricsResource(makePayload("svc", "", "", nil), false, false, "")
	assert.NotContains(t, kvAttrsToMap(res.Attributes), "datadog.runtime_id")
}

func TestBuildMetricsResourceOtelModeSuppressesDatadogAttrs(t *testing.T) {
	// OTel mode must not emit any datadog.* resource attributes (process tags, runtime ID, etc.).
	payload := makePayload("svc", "", "", nil)
	payload.ProcessTags = "entrypoint.name:myapp"
	payload.RuntimeID = "abc-123"
	res := buildMetricsResource(payload, true /* otelMode */, false, "")
	m := kvAttrsToMap(res.Attributes)
	assert.NotContains(t, m, "datadog.process_tags")
	assert.NotContains(t, m, "datadog.runtime_id")
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
	assert.Equal(t, "SPAN_KIND_SERVER", m["span.kind"])
	assert.Equal(t, "GET", m["http.request.method"])
	assert.Equal(t, "200", m["http.response.status_code"])
	assert.NotContains(t, m, "datadog.operation.name")
	assert.NotContains(t, m, "datadog.span.type")
	assert.NotContains(t, m, "datadog.span.top_level")
}

func TestDataPointAttributesDefaultMode(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Name:         "web.request",
		Resource:     "/users",
		Type:         "web",
		Hits:         1,
		TopLevelHits: 1,
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, false /* default mode */))
	assert.Equal(t, "web.request", m["datadog.operation.name"])
	assert.Equal(t, "web", m["datadog.span.type"])
	assert.Equal(t, "true", m["datadog.span.top_level"])
}

func TestDataPointAttributesTopLevelFalse(t *testing.T) {
	t.Run("no top-level spans in group", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "child-resource", Hits: 1, TopLevelHits: 0}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, false))
		assert.Equal(t, "false", m["datadog.span.top_level"])
	})
	t.Run("mixed group conservatively non-top-level", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "mixed", Hits: 10, TopLevelHits: 5}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, false))
		assert.Equal(t, "false", m["datadog.span.top_level"])
	})
}

func TestDataPointAttributesStatusCode(t *testing.T) {
	gs := &pb.ClientGroupedStats{Resource: "/ok"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false /* isError */, true))
	assert.Equal(t, "STATUS_CODE_OK", m["status.code"])

	gs = &pb.ClientGroupedStats{Resource: "/err"}
	m = kvAttrsToMap(buildDataPointAttributes(gs, true /* isError */, true))
	assert.Equal(t, "STATUS_CODE_ERROR", m["status.code"])
}

func TestDataPointAttributesIsTraceRoot(t *testing.T) {
	gs := &pb.ClientGroupedStats{Resource: "root", IsTraceRoot: pb.Trilean_TRUE}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, false /* default mode */))
	assert.Equal(t, "true", m["datadog.is_trace_root"])

	gs = &pb.ClientGroupedStats{Resource: "child", IsTraceRoot: pb.Trilean_FALSE}
	m = kvAttrsToMap(buildDataPointAttributes(gs, false, false))
	assert.Equal(t, "false", m["datadog.is_trace_root"])

	gs = &pb.ClientGroupedStats{Resource: "unknown", IsTraceRoot: pb.Trilean_NOT_SET}
	m = kvAttrsToMap(buildDataPointAttributes(gs, false, false))
	assert.NotContains(t, m, "datadog.is_trace_root")
}

func TestDataPointAttributesAdditionalMetricTags(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Resource:             "/users",
		AdditionalMetricTags: []string{"customer.tier:gold", "region:us-east-1", "malformed", "empty:"},
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true /* otelMode: not gated */))
	assert.Equal(t, "gold", m["customer.tier"])
	assert.Equal(t, "us-east-1", m["region"])
	assert.NotContains(t, m, "malformed")
	assert.NotContains(t, m, "empty")
}

func TestDataPointAttributesHTTPRoute(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Resource:     "web.request",
		HTTPEndpoint: "/users/{id}",
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
	assert.Equal(t, "/users/{id}", m["http.route"])
}

func TestDataPointAttributesOptionalFieldsAbsentWhenUnset(t *testing.T) {
	// Optional OTel attributes are omitted when the corresponding source field is zero/empty.
	gs := &pb.ClientGroupedStats{Resource: "op"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
	assert.NotContains(t, m, "http.route")
	assert.NotContains(t, m, "rpc.response.status_code")
}

func TestDataPointAttributesPeerTags(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Resource: "postgres.query",
		PeerTags: []string{"db.hostname:prod-db-1"},
	}
	values := kvArrayValue(buildDataPointAttributes(gs, false, false /* default mode */), "datadog.peer_tags")
	assert.Contains(t, values, "db.hostname:prod-db-1")

	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true /* otelMode */))
	assert.NotContains(t, m, "datadog.peer_tags")
}

func TestDataPointAttributesGRPCStatusCode(t *testing.T) {
	// The concentrator stores numeric code strings; we reverse-map to canonical names.
	for code, name := range map[string]string{
		"0":  "OK",
		"5":  "NOT_FOUND",
		"14": "UNAVAILABLE",
	} {
		gs := &pb.ClientGroupedStats{Resource: "grpc.request", GRPCStatusCode: code}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
		assert.Equal(t, name, m["rpc.response.status_code"], "code %s", code)
	}
	// Unknown numeric code: keep as integer (kvAttrsToMap renders it as a decimal string).
	gs := &pb.ClientGroupedStats{Resource: "grpc.request", GRPCStatusCode: "99"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
	assert.Equal(t, "99", m["rpc.response.status_code"])
}

func TestDataPointAttributesSyntheticsOrigin(t *testing.T) {
	// Synthetics=true emits datadog.origin=synthetics in default mode.
	gs := &pb.ClientGroupedStats{Resource: "web.request", Synthetics: true}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, false /* default mode */))
	assert.Equal(t, "synthetics", m["datadog.origin"])
}

func TestDataPointAttributesSyntheticsOriginOmitted(t *testing.T) {
	t.Run("otel mode", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "web.request", Synthetics: true}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
		assert.NotContains(t, m, "datadog.origin")
	})
	t.Run("synthetics false", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "web.request", Synthetics: false}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, false))
		assert.NotContains(t, m, "datadog.origin")
	})
}

func TestDataPointAttributesServiceName(t *testing.T) {
	t.Run("non-default service carries service.name on data point", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Service: "postgres", Resource: "SELECT"}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, false))
		assert.Equal(t, "postgres", m["service.name"])
	})
	t.Run("service matching the default/global service is still emitted", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Service: "my-app", Resource: "web.request"}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, false))
		assert.Equal(t, "my-app", m["service.name"])
	})
	t.Run("emitted in OTel-semantics mode too", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Service: "my-app", Resource: "web.request"}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, true /* otelMode */))
		assert.Equal(t, "my-app", m["service.name"])
	})
}

func TestDataPointCountEqualsBucketCountSum(t *testing.T) {
	// OTLP requires sum(BucketCounts) == Count. Because both come from the sketch,
	// the invariant holds by construction — this test locks it in.
	cfg := internalconfig.CreateNew()
	gs := &pb.ClientGroupedStats{
		Resource:     "/users",
		Hits:         5,
		Errors:       2,
		OkSummary:    encodeSketch(t, 1e6, 5e6, 50e6),
		ErrorSummary: encodeSketch(t, 100e6, 500e6),
	}
	rm := buildOTLPMetricsRequest(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}), cfg)
	require.NotNil(t, rm)
	for _, dp := range rm[0].ScopeMetrics[0].Metrics[0].GetHistogram().DataPoints {
		var bucketSum uint64
		for _, c := range dp.BucketCounts {
			bucketSum += c
		}
		assert.Equal(t, dp.Count, bucketSum, "sum(BucketCounts) must equal Count")
	}
}

func TestBuildMetricsResourceHostnamePresent(t *testing.T) {
	res := buildMetricsResource(makePayload("svc", "", "", nil), false, true, "myhost")
	assert.Equal(t, "myhost", kvAttrsToMap(res.Attributes)["host.name"])
}

func TestBuildOTLPMetricsRequestMultiBucket(t *testing.T) {
	// Two stat buckets with distinct time windows must produce data points with distinct timestamps.
	cfg := internalconfig.CreateNew()
	newGroup := func() *pb.ClientGroupedStats {
		return &pb.ClientGroupedStats{Resource: "/ping", Hits: 1, OkSummary: encodeSketch(t, 10e6)}
	}
	bucket1Start := uint64(1_000_000_000)
	bucket2Start := uint64(2_000_000_000)
	bucketDur := uint64(10_000_000_000)
	payload := &pb.ClientStatsPayload{
		Service: "svc",
		Stats: []*pb.ClientStatsBucket{
			{Start: bucket1Start, Duration: bucketDur, Stats: []*pb.ClientGroupedStats{newGroup()}},
			{Start: bucket2Start, Duration: bucketDur, Stats: []*pb.ClientGroupedStats{newGroup()}},
		},
	}
	rm := buildOTLPMetricsRequest(payload, cfg)
	require.NotNil(t, rm)
	dps := rm[0].ScopeMetrics[0].Metrics[0].GetHistogram().DataPoints
	require.Len(t, dps, 2)
	assert.Equal(t, bucket1Start, dps[0].StartTimeUnixNano)
	assert.Equal(t, bucket1Start+bucketDur, dps[0].TimeUnixNano)
	assert.Equal(t, bucket2Start, dps[1].StartTimeUnixNano)
	assert.Equal(t, bucket2Start+bucketDur, dps[1].TimeUnixNano)
}

func TestDataPointAttributesGRPCStatusCodeStringFallback(t *testing.T) {
	// Non-numeric GRPCStatusCode is malformed; it is dropped rather than emitting a
	// value that would change rpc.response.status_code's type across data points.
	gs := &pb.ClientGroupedStats{Resource: "grpc.request", GRPCStatusCode: "CUSTOM_STATUS"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, true))
	assert.NotContains(t, m, "rpc.response.status_code")
}
