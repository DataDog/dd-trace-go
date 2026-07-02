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
			m[kv.Key] = fmt.Sprintf("%v", v.BoolValue)
		case *otlpcommon.AnyValue_IntValue:
			m[kv.Key] = fmt.Sprintf("%d", v.IntValue)
		case *otlpcommon.AnyValue_DoubleValue:
			m[kv.Key] = fmt.Sprintf("%g", v.DoubleValue)
		}
	}
	return m
}

// ---- sketchToHistogram ----

func TestSketchToHistogramEmpty(t *testing.T) {
	sk, err := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
	require.NoError(t, err)
	b, err := proto.Marshal(sk.ToProto())
	require.NoError(t, err)
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
		Hits:         1,
		TopLevelHits: 1,
		OkSummary:    encodeSketch(t, 50e6), // 50ms
	}
	rm := BuildOTLPMetricsRequest(makePayload("svc", "prod", "1.0", []*pb.ClientGroupedStats{gs}), cfg)
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
		Hits:         2,
		Errors:       1,
		OkSummary:    encodeSketch(t, 50e6),
		ErrorSummary: encodeSketch(t, 100e6),
	}
	rm := BuildOTLPMetricsRequest(makePayload("svc", "", "", []*pb.ClientGroupedStats{gs}), cfg)
	require.NotNil(t, rm)
	hist := rm[0].ScopeMetrics[0].Metrics[0].GetHistogram()
	require.Len(t, hist.DataPoints, 2)
}

func TestBuildOTLPMetricsRequestMultipleServices(t *testing.T) {
	// Multiple services in one payload share a single scope; the non-default service
	// carries service.name as a data-point attribute.
	cfg := internalconfig.CreateNew()
	gs1 := &pb.ClientGroupedStats{Service: "svc-a", Resource: "res-a", OkSummary: encodeSketch(t, 50e6)}
	gs2 := &pb.ClientGroupedStats{Service: "svc-b", Resource: "res-b", OkSummary: encodeSketch(t, 100e6)}
	rm := BuildOTLPMetricsRequest(makePayload("svc-a", "", "", []*pb.ClientGroupedStats{gs1, gs2}), cfg)
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
	assert.NotContains(t, kvAttrsToMap(svcAPoint.Attributes), "service.name", "default service omits service.name on data point")
	assert.Equal(t, "svc-b", kvAttrsToMap(svcBPoint.Attributes)["service.name"], "non-default service carries service.name on data point")
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
	m := kvAttrsToMap(res.Attributes)
	assert.Equal(t, "myapp", m["datadog.entrypoint.name"])
	assert.Equal(t, "binary", m["datadog.entrypoint.type"])
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
	assert.NotContains(t, m, "datadog.entrypoint.name")
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
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "" /* defaultService */, true /* otelMode */))
	assert.Equal(t, "/users", m["span.name"])
	assert.Equal(t, "server", m["span.kind"])
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
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "" /* defaultService */, false /* default mode */))
	assert.Equal(t, "web.request", m["datadog.operation.name"])
	assert.Equal(t, "web", m["datadog.span.type"])
	assert.Equal(t, "true", m["datadog.span.top_level"])
}

func TestDataPointAttributesTopLevelFalse(t *testing.T) {
	t.Run("no top-level spans in group", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "child-resource", Hits: 1, TopLevelHits: 0}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", false))
		assert.Equal(t, "false", m["datadog.span.top_level"])
	})
	t.Run("mixed group conservatively non-top-level", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "mixed", Hits: 10, TopLevelHits: 5}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", false))
		assert.Equal(t, "false", m["datadog.span.top_level"])
	})
}

func TestDataPointAttributesErrorStatusCode(t *testing.T) {
	gs := &pb.ClientGroupedStats{Resource: "/err"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, true /* isError */, "", true))
	assert.Equal(t, "2", m["status.code"])
}

func TestDataPointAttributesHTTPRoute(t *testing.T) {
	gs := &pb.ClientGroupedStats{
		Resource:     "web.request",
		HTTPEndpoint: "/users/{id}",
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.Equal(t, "/users/{id}", m["http.route"])
}

func TestDataPointAttributesOptionalFieldsAbsentWhenUnset(t *testing.T) {
	// Optional OTel attributes are omitted when the corresponding source field is zero/empty.
	gs := &pb.ClientGroupedStats{Resource: "op"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.NotContains(t, m, "http.route")
	assert.NotContains(t, m, "rpc.response.status_code")
}

func TestDataPointAttributesGRPCMethodName(t *testing.T) {
	// grpc.method.name in PeerTags is translated to the RFC-required rpc.method attribute.
	gs := &pb.ClientGroupedStats{
		Resource: "grpc.request",
		PeerTags: []string{"grpc.method.name:GetUser"},
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.Equal(t, "GetUser", m["rpc.method"])
	assert.NotContains(t, m, "grpc.method.name")
}

func TestDataPointAttributesGRPCMethodNameFirstOnly(t *testing.T) {
	// Only the first grpc.method.name value is used; no duplicates emitted.
	gs := &pb.ClientGroupedStats{
		Resource: "grpc.request",
		PeerTags: []string{"grpc.method.name:GetUser", "grpc.method.name:ListUsers"},
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.Equal(t, "GetUser", m["rpc.method"])
}

func TestDataPointAttributesGRPCMethodNameEmptySkipped(t *testing.T) {
	// A grpc.method.name tag with an empty value must not emit rpc.method.
	gs := &pb.ClientGroupedStats{
		Resource: "grpc.request",
		PeerTags: []string{"grpc.method.name:"},
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.NotContains(t, m, "rpc.method")
}

func TestDataPointAttributesPeerTagsNotEmitted(t *testing.T) {
	// peer.* tags and other non-grpc.method.name peer tags are not forwarded (out of scope per RFC).
	gs := &pb.ClientGroupedStats{
		Resource: "web.request",
		PeerTags: []string{"peer.service:db", "db.system:postgresql"},
	}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.NotContains(t, m, "peer.service")
	assert.NotContains(t, m, "db.system")
}

func TestDataPointAttributesGRPCStatusCode(t *testing.T) {
	// GRPCStatusCode is emitted as rpc.response.status_code (integer when parseable).
	gs := &pb.ClientGroupedStats{Resource: "grpc.request", GRPCStatusCode: "0"}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
	assert.Equal(t, "0", m["rpc.response.status_code"])
}

func TestDataPointAttributesSyntheticsOrigin(t *testing.T) {
	// Synthetics=true emits datadog.origin=synthetics in default mode.
	gs := &pb.ClientGroupedStats{Resource: "web.request", Synthetics: true}
	m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", false /* default mode */))
	assert.Equal(t, "synthetics", m["datadog.origin"])
}

func TestDataPointAttributesSyntheticsOriginOmitted(t *testing.T) {
	t.Run("otel mode", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "web.request", Synthetics: true}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", true))
		assert.NotContains(t, m, "datadog.origin")
	})
	t.Run("synthetics false", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Resource: "web.request", Synthetics: false}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, "", false))
		assert.NotContains(t, m, "datadog.origin")
	})
}

func TestDataPointAttributesServiceName(t *testing.T) {
	t.Run("non-default service carries service.name on data point", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Service: "postgres", Resource: "SELECT"}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, "my-app", false))
		assert.Equal(t, "postgres", m["service.name"])
	})
	t.Run("default service omits service.name on data point", func(t *testing.T) {
		gs := &pb.ClientGroupedStats{Service: "my-app", Resource: "web.request"}
		m := kvAttrsToMap(buildDataPointAttributes(gs, false, "my-app", false))
		assert.NotContains(t, m, "service.name")
	})
}
