// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"math"
	"sort"
	"strconv"
	"strings"

	ddsketch "github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/pb/sketchpb"
	"google.golang.org/protobuf/proto"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const spanDurationMetricName = "traces.span.sdk.metrics.duration"

// grpcStatusCodeByNumber maps the numeric gRPC status codes that the stats
// concentrator stores in ClientGroupedStats.GRPCStatusCode back to their
// canonical string names expected by the span metrics connector.
var grpcStatusCodeByNumber = map[int64]string{
	0: "OK", 1: "CANCELLED", 2: "UNKNOWN", 3: "INVALID_ARGUMENT",
	4: "DEADLINE_EXCEEDED", 5: "NOT_FOUND", 6: "ALREADY_EXISTS",
	7: "PERMISSION_DENIED", 8: "RESOURCE_EXHAUSTED", 9: "FAILED_PRECONDITION",
	10: "ABORTED", 11: "OUT_OF_RANGE", 12: "UNIMPLEMENTED", 13: "INTERNAL",
	14: "UNAVAILABLE", 15: "DATA_LOSS", 16: "UNAUTHENTICATED",
}

// otelSpanKindByDDSpanKind maps Datadog span-kind values to the OTel Span Metrics Connector's
// SPAN_KIND_* string convention.
var otelSpanKindByDDSpanKind = map[string]string{
	ext.SpanKindServer:   "SPAN_KIND_SERVER",
	ext.SpanKindClient:   "SPAN_KIND_CLIENT",
	ext.SpanKindProducer: "SPAN_KIND_PRODUCER",
	ext.SpanKindConsumer: "SPAN_KIND_CONSUMER",
	ext.SpanKindInternal: "SPAN_KIND_INTERNAL",
}

// statusCodeError is the OTel Span Metrics Connector's string convention for an error status.code attribute.
const statusCodeError = "STATUS_CODE_ERROR"

// spanMetricBounds are histogram bucket boundaries (seconds), matching OTel Span Metrics Connector defaults.
var spanMetricBounds = [16]float64{0.002, 0.004, 0.006, 0.008, 0.01, 0.05, 0.1, 0.2, 0.4, 0.8, 1, 1.4, 2, 5, 10, 15}

// buildOTLPMetricsRequest converts a ClientStatsPayload to OTLP ResourceMetrics (DELTA histogram).
// Returns nil when empty.
func buildOTLPMetricsRequest(payload *pb.ClientStatsPayload, cfg *internalconfig.Config) []*otlpmetrics.ResourceMetrics {
	var allPoints []*otlpmetrics.HistogramDataPoint
	for _, bucket := range payload.Stats {
		bucketEnd := bucket.Start + bucket.Duration
		for _, gs := range bucket.Stats {
			pts := buildGroupDataPoints(gs, bucket.Start, bucketEnd)
			allPoints = append(allPoints, pts...)
		}
	}

	if len(allPoints) == 0 {
		return nil
	}

	resource := buildMetricsResource(payload, cfg.ReportHostname(), cfg.Hostname())

	scopeMetrics := []*otlpmetrics.ScopeMetrics{
		{
			Metrics: []*otlpmetrics.Metric{
				{
					Name: spanDurationMetricName,
					Unit: "s",
					Data: &otlpmetrics.Metric_Histogram{
						Histogram: &otlpmetrics.Histogram{
							AggregationTemporality: otlpmetrics.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
							DataPoints:             allPoints,
						},
					},
				},
			},
		},
	}

	return []*otlpmetrics.ResourceMetrics{
		{
			Resource:     resource,
			ScopeMetrics: scopeMetrics,
		},
	}
}

// buildMetricsResource builds the OTLP Resource.
func buildMetricsResource(payload *pb.ClientStatsPayload, reportHostname bool, hostname string) *otlpresource.Resource {
	attrs := buildBaseResourceAttrs(payload.Service, payload.Version, payload.Env)
	if reportHostname && hostname != "" {
		attrs = append(attrs, otlpKeyValue("host.name", otlpStringValue(hostname)))
	}
	if payload.RuntimeID != "" {
		attrs = append(attrs, otlpKeyValue("datadog.runtime_id", otlpStringValue(payload.RuntimeID)))
	}
	if payload.ProcessTags != "" {
		// Mirrors the same comma-separated ProcessTags string the /v0.6/stats (ddStatsSender) path sends as-is; update both if that shape changes.
		tags := strings.Split(payload.ProcessTags, ",")
		attrs = append(attrs, otlpKeyValue("datadog.process_tags", otlpStringArrayValue(tags)))
	}
	return &otlpresource.Resource{Attributes: attrs}
}

// buildGroupDataPoints produces up to two OTLP histogram data points (ok + error) from a ClientGroupedStats.
func buildGroupDataPoints(gs *pb.ClientGroupedStats, startNs, endNs uint64) []*otlpmetrics.HistogramDataPoint {
	var pts []*otlpmetrics.HistogramDataPoint
	if len(gs.OkSummary) > 0 {
		if dp := decodeAndBuildDataPoint(gs, gs.OkSummary, startNs, endNs, false); dp != nil {
			pts = append(pts, dp)
		}
	}
	if len(gs.ErrorSummary) > 0 {
		if dp := decodeAndBuildDataPoint(gs, gs.ErrorSummary, startNs, endNs, true); dp != nil {
			pts = append(pts, dp)
		}
	}
	return pts
}

func decodeAndBuildDataPoint(gs *pb.ClientGroupedStats, sketchBytes []byte, startNs, endNs uint64, isError bool) *otlpmetrics.HistogramDataPoint {
	bucketCounts, sum, minSec, maxSec, count, err := sketchToHistogram(sketchBytes, spanMetricBounds[:])
	if err != nil {
		log.Warn("stats_to_otlp_metrics: failed to decode sketch: %v", err.Error())
		return nil
	}
	if count == 0 {
		return nil
	}
	// count comes from the sketch so sum(BucketCounts) == Count by construction,
	// satisfying the OTLP histogram invariant.
	dp := &otlpmetrics.HistogramDataPoint{
		StartTimeUnixNano: startNs,
		TimeUnixNano:      endNs,
		Count:             count,
		Sum:               &sum,
		Min:               &minSec,
		Max:               &maxSec,
		ExplicitBounds:    spanMetricBounds[:],
		BucketCounts:      bucketCounts,
		Attributes:        buildDataPointAttributes(gs, isError),
	}
	return dp
}

// buildDataPointAttributes returns OTLP data-point attributes.
func buildDataPointAttributes(gs *pb.ClientGroupedStats, isError bool) []*otlpcommon.KeyValue {
	var attrs []*otlpcommon.KeyValue

	// OTel semantic-convention attributes.
	// Resource (e.g. "GET /users/{id}") maps to span.name.
	if gs.Resource != "" {
		attrs = append(attrs, otlpKeyValue("span.name", otlpStringValue(gs.Resource)))
	}
	spanKind := "SPAN_KIND_INTERNAL"
	if canonical, ok := otelSpanKindByDDSpanKind[gs.SpanKind]; ok {
		spanKind = canonical
	}
	attrs = append(attrs, otlpKeyValue("span.kind", otlpStringValue(spanKind)))
	if gs.HTTPMethod != "" {
		attrs = append(attrs, otlpKeyValue("http.request.method", otlpStringValue(gs.HTTPMethod)))
	}
	if gs.HTTPStatusCode != 0 {
		attrs = append(attrs, otlpKeyValue("http.response.status_code", otlpIntValue(int64(gs.HTTPStatusCode))))
	}
	if gs.HTTPEndpoint != "" {
		attrs = append(attrs, otlpKeyValue("http.route", otlpStringValue(gs.HTTPEndpoint)))
	}
	if gs.GRPCStatusCode != "" {
		if code, err := strconv.ParseInt(gs.GRPCStatusCode, 10, 64); err == nil {
			if name, ok := grpcStatusCodeByNumber[code]; ok {
				attrs = append(attrs, otlpKeyValue("rpc.response.status_code", otlpStringValue(name)))
			} else {
				// Unknown code outside 0-16: emit the numeric string to keep the attribute type
				// stable (always stringValue) across all data points.
				attrs = append(attrs, otlpKeyValue("rpc.response.status_code", otlpStringValue(gs.GRPCStatusCode)))
			}
		}
		// Non-numeric values are malformed for gRPC and are silently dropped rather than
		// emitting a value that would change the attribute's type.
	}
	if isError {
		attrs = append(attrs, otlpKeyValue("status.code", otlpStringValue(statusCodeError)))
	}

	attrs = append(attrs, otlpKeyValue("service.name", otlpStringValue(gs.Service)))

	// additional_metric_tags support is still evolving/TBD across most SDKs.
	for _, tag := range gs.AdditionalMetricTags {
		key, value, ok := strings.Cut(tag, ":")
		if !ok || key == "" || value == "" {
			continue
		}
		attrs = append(attrs, otlpKeyValue(key, otlpStringValue(value)))
	}

	if gs.Name != "" {
		attrs = append(attrs, otlpKeyValue("datadog.operation.name", otlpStringValue(gs.Name)))
	}
	if gs.Type != "" {
		attrs = append(attrs, otlpKeyValue("datadog.span.type", otlpStringValue(gs.Type)))
	}
	// top_level is true only when all spans in the group were top-level (TopLevelHits == Hits).
	attrs = append(attrs, otlpKeyValue("datadog.span.top_level", otlpBoolValue(gs.Hits > 0 && gs.TopLevelHits == gs.Hits)))
	switch gs.IsTraceRoot {
	case pb.Trilean_TRUE:
		attrs = append(attrs, otlpKeyValue("datadog.is_trace_root", otlpBoolValue(true)))
	case pb.Trilean_FALSE:
		attrs = append(attrs, otlpKeyValue("datadog.is_trace_root", otlpBoolValue(false)))
	}
	if len(gs.PeerTags) > 0 {
		attrs = append(attrs, otlpKeyValue("datadog.peer_tags", otlpStringArrayValue(gs.PeerTags)))
	}
	if gs.ServiceSource != "" {
		attrs = append(attrs, otlpKeyValue("datadog.svc_src", otlpStringValue(gs.ServiceSource)))
	}
	// ClientGroupedStats carries only a boolean Synthetics field; finer-grained
	// origin values (synthetics-browser, rum, ciapp-test, lambda) are not available
	// at the stats aggregation layer and require a proto change upstream to support.
	if gs.Synthetics {
		attrs = append(attrs, otlpKeyValue("datadog.origin", otlpStringValue("synthetics")))
	}

	return attrs
}

func otlpStringArrayValue(values []string) *otlpcommon.AnyValue {
	items := make([]*otlpcommon.AnyValue, 0, len(values))
	for _, v := range values {
		items = append(items, otlpStringValue(v))
	}
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_ArrayValue{ArrayValue: &otlpcommon.ArrayValue{Values: items}}}
}

// sketchToHistogram decodes a proto-marshaled DDSketch (values in ns) and maps it into histogram
// buckets (seconds), returning (bucketCounts, sum, min, max, count, error).
func sketchToHistogram(sketchBytes []byte, bounds []float64) ([]uint64, float64, float64, float64, uint64, error) {
	var skPb sketchpb.DDSketch
	if err := proto.Unmarshal(sketchBytes, &skPb); err != nil {
		return nil, 0, 0, 0, 0, err
	}
	sketch, err := ddsketch.FromProto(&skPb)
	if err != nil {
		return nil, 0, 0, 0, 0, err
	}
	if sketch.GetCount() == 0 {
		return nil, 0, 0, 0, 0, nil
	}

	buckets := make([]uint64, len(bounds)+1)
	var sumSec float64
	var totalCount uint64

	sketch.ForEach(func(valueNs, count float64) bool {
		valueSec := valueNs / 1e9
		c := uint64(math.Round(count))
		totalCount += c
		sumSec += valueSec * count
		// OTLP: bucket i covers (bounds[i-1], bounds[i]], upper-bound inclusive.
		idx := sort.Search(len(bounds), func(i int) bool { return bounds[i] >= valueSec })
		buckets[idx] += c
		return false
	})

	var minSec, maxSec float64
	if minNs, err := sketch.GetMinValue(); err == nil {
		minSec = minNs / 1e9
	}
	if maxNs, err := sketch.GetMaxValue(); err == nil {
		maxSec = maxNs / 1e9
	}

	return buckets, sumSec, minSec, maxSec, totalCount, nil
}
