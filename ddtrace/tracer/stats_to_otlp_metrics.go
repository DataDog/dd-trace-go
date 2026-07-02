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

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const spanDurationMetricName = "traces.span.sdk.metrics.duration"

// spanMetricBounds are histogram bucket boundaries (seconds), matching OTel Span Metrics Connector defaults.
var spanMetricBounds = [16]float64{0.002, 0.004, 0.006, 0.008, 0.01, 0.05, 0.1, 0.2, 0.4, 0.8, 1, 1.4, 2, 5, 10, 15}

// buildOTLPMetricsRequest converts a ClientStatsPayload to OTLP ResourceMetrics (DELTA histogram).
// Non-default services carry service.name as a data-point attribute. Returns nil when empty.
func buildOTLPMetricsRequest(payload *pb.ClientStatsPayload, cfg *internalconfig.Config) []*otlpmetrics.ResourceMetrics {
	otelMode := cfg.OTLPSemanticsMode()

	var allPoints []*otlpmetrics.HistogramDataPoint
	for _, bucket := range payload.Stats {
		bucketEnd := bucket.Start + bucket.Duration
		for _, gs := range bucket.Stats {
			pts := buildGroupDataPoints(gs, bucket.Start, bucketEnd, payload.Service, otelMode)
			allPoints = append(allPoints, pts...)
		}
	}

	if len(allPoints) == 0 {
		return nil
	}

	resource := buildMetricsResource(payload, otelMode, cfg.ReportHostname(), cfg.Hostname())

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

// buildMetricsResource builds the OTLP Resource; adds host.name and datadog.* attrs in default mode.
func buildMetricsResource(payload *pb.ClientStatsPayload, otelMode bool, reportHostname bool, hostname string) *otlpresource.Resource {
	attrs := []*otlpcommon.KeyValue{
		otlpKeyValue("telemetry.sdk.language", otlpStringValue("go")),
		otlpKeyValue("telemetry.sdk.name", otlpStringValue("datadog")),
		otlpKeyValue("telemetry.sdk.version", otlpStringValue(version.Tag)),
		otlpKeyValue("service.name", otlpStringValue(payload.Service)),
	}
	if payload.Version != "" {
		attrs = append(attrs, otlpKeyValue("service.version", otlpStringValue(payload.Version)))
	}
	if payload.Env != "" {
		attrs = append(attrs, otlpKeyValue("deployment.environment.name", otlpStringValue(payload.Env)))
	}
	if reportHostname {
		if hostname != "" {
			attrs = append(attrs, otlpKeyValue("host.name", otlpStringValue(hostname)))
		}
	}
	if !otelMode {
		if payload.RuntimeID != "" {
			attrs = append(attrs, otlpKeyValue("datadog.runtime_id", otlpStringValue(payload.RuntimeID)))
		}
		if payload.ProcessTags != "" {
			for tag := range strings.SplitSeq(payload.ProcessTags, ",") {
				parts := strings.SplitN(tag, ":", 2)
				if len(parts) == 2 && parts[0] != "" {
					attrs = append(attrs, otlpKeyValue("datadog."+parts[0], otlpStringValue(parts[1])))
				}
			}
		}
	}
	return &otlpresource.Resource{Attributes: attrs}
}

// buildGroupDataPoints produces up to two OTLP histogram data points (ok + error) from a ClientGroupedStats.
// Non-default services carry service.name as a data-point attribute.
func buildGroupDataPoints(gs *pb.ClientGroupedStats, startNs, endNs uint64, defaultService string, otelMode bool) []*otlpmetrics.HistogramDataPoint {
	var pts []*otlpmetrics.HistogramDataPoint

	okCount := uint64(0)
	if gs.Hits >= gs.Errors {
		okCount = gs.Hits - gs.Errors
	}
	if len(gs.OkSummary) > 0 {
		if dp := decodeAndBuildDataPoint(gs, gs.OkSummary, startNs, endNs, false, okCount, defaultService, otelMode); dp != nil {
			pts = append(pts, dp)
		}
	}
	if len(gs.ErrorSummary) > 0 {
		if dp := decodeAndBuildDataPoint(gs, gs.ErrorSummary, startNs, endNs, true, gs.Errors, defaultService, otelMode); dp != nil {
			pts = append(pts, dp)
		}
	}
	return pts
}

func decodeAndBuildDataPoint(gs *pb.ClientGroupedStats, sketchBytes []byte, startNs, endNs uint64, isError bool, exactCount uint64, defaultService string, otelMode bool) *otlpmetrics.HistogramDataPoint {
	bucketCounts, sum, minSec, maxSec, sketchCount, err := sketchToHistogram(sketchBytes, spanMetricBounds[:])
	if err != nil {
		log.Error("stats_to_otlp_metrics: failed to decode sketch: %v", err.Error())
		return nil
	}
	if sketchCount == 0 {
		return nil
	}
	count := exactCount
	if count == 0 {
		count = sketchCount
	}

	dp := &otlpmetrics.HistogramDataPoint{
		StartTimeUnixNano: startNs,
		TimeUnixNano:      endNs,
		Count:             count,
		Sum:               &sum,
		Min:               &minSec,
		Max:               &maxSec,
		ExplicitBounds:    spanMetricBounds[:],
		BucketCounts:      bucketCounts,
		Attributes:        buildDataPointAttributes(gs, isError, defaultService, otelMode),
	}
	return dp
}

// buildDataPointAttributes returns OTLP data-point attributes; adds service.name for non-default services.
func buildDataPointAttributes(gs *pb.ClientGroupedStats, isError bool, defaultService string, otelMode bool) []*otlpcommon.KeyValue {
	var attrs []*otlpcommon.KeyValue

	// OTel semantic-convention attributes.
	if gs.Resource != "" {
		attrs = append(attrs, otlpKeyValue("span.name", otlpStringValue(gs.Resource)))
	}
	if gs.SpanKind != "" {
		attrs = append(attrs, otlpKeyValue("span.kind", otlpStringValue(gs.SpanKind)))
	}
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
			attrs = append(attrs, otlpKeyValue("rpc.response.status_code", otlpIntValue(code)))
		} else {
			attrs = append(attrs, otlpKeyValue("rpc.response.status_code", otlpStringValue(gs.GRPCStatusCode)))
		}
	}
	statusCode := "Ok"
	if isError {
		statusCode = "Error"
	}
	attrs = append(attrs, otlpKeyValue("status.code", otlpStringValue(statusCode)))
	// grpc.method.name arrives via PeerTags (no dedicated field in ClientGroupedStats) and maps to rpc.method.
	for _, tag := range gs.PeerTags {
		if k, v, ok := strings.Cut(tag, ":"); ok && k == "grpc.method.name" && v != "" {
			attrs = append(attrs, otlpKeyValue("rpc.method", otlpStringValue(v)))
			break
		}
	}

	if svc := gs.Service; svc != "" && svc != defaultService {
		attrs = append(attrs, otlpKeyValue("service.name", otlpStringValue(svc)))
	}

	// Datadog-specific attributes (default mode only).
	if !otelMode {
		if gs.Name != "" {
			attrs = append(attrs, otlpKeyValue("datadog.operation.name", otlpStringValue(gs.Name)))
		}
		if gs.Type != "" {
			attrs = append(attrs, otlpKeyValue("datadog.span.type", otlpStringValue(gs.Type)))
		}
		// top_level is true only when all spans in the group were top-level (TopLevelHits == Hits).
		attrs = append(attrs, otlpKeyValue("datadog.span.top_level", otlpBoolValue(gs.Hits > 0 && gs.TopLevelHits == gs.Hits)))
		if gs.Synthetics {
			attrs = append(attrs, otlpKeyValue("datadog.origin", otlpStringValue("synthetics")))
		}
	}

	return attrs
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
