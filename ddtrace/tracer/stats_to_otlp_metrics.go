// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"math"
	"sort"
	"strings"

	ddsketch "github.com/DataDog/sketches-go/ddsketch"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpcollectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const spanDurationMetricName = "traces.span.sdk.metrics.duration"

// spanMetricBounds are the fixed histogram bucket boundaries in seconds (16 boundaries, 17 buckets).
// These match the OTel Span Metrics Connector defaults, scaled from milliseconds to seconds.
var spanMetricBounds = []float64{0.002, 0.004, 0.006, 0.008, 0.01, 0.05, 0.1, 0.2, 0.4, 0.8, 1, 1.4, 2, 5, 10, 15}

// BuildOTLPMetricsRequest converts a ClientStatsPayload into an ExportMetricsServiceRequest.
// Service identity (service.name, service.version, deployment.environment.name) is placed on the
// Resource. A single InstrumentationScope is used for all data points. When a grouped-stats entry
// carries a different service than the payload's configured default, service.name is additionally
// emitted as a data-point attribute. The resulting histogram uses DELTA temporality.
func BuildOTLPMetricsRequest(payload *pb.ClientStatsPayload, cfg *internalconfig.Config) *otlpcollectormetrics.ExportMetricsServiceRequest {
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

	resource := buildMetricsResource(cfg, payload, otelMode)

	scopeMetrics := []*otlpmetrics.ScopeMetrics{
		{
			Scope: &otlpcommon.InstrumentationScope{
				Name:    "dd-trace-go",
				Version: version.Tag,
			},
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

	return &otlpcollectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*otlpmetrics.ResourceMetrics{
			{
				Resource:     resource,
				ScopeMetrics: scopeMetrics,
			},
		},
	}
}

// buildMetricsResource constructs the OTLP Resource for the metrics payload.
// Includes SDK identification, service identity, optional host.name, and datadog.* process-tag
// attributes (default mode only).
func buildMetricsResource(cfg *internalconfig.Config, payload *pb.ClientStatsPayload, otelMode bool) *otlpresource.Resource {
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
	if cfg.ReportHostname() {
		if h := cfg.Hostname(); h != "" {
			attrs = append(attrs, otlpKeyValue("host.name", otlpStringValue(h)))
		}
	}
	if !otelMode && payload.ProcessTags != "" {
		for _, tag := range strings.Split(payload.ProcessTags, ",") {
			parts := strings.SplitN(tag, ":", 2)
			if len(parts) == 2 && parts[0] != "" {
				attrs = append(attrs, otlpKeyValue("datadog."+parts[0], otlpStringValue(parts[1])))
			}
		}
	}
	return &otlpresource.Resource{Attributes: attrs}
}

// buildGroupDataPoints builds histogram data points for a single ClientGroupedStats group.
// It produces one data point for OK spans (OkSummary) and one for error spans (ErrorSummary)
// when those summaries are non-empty. defaultService is the payload's configured service name;
// when gs.Service differs from it, service.name is added as a data-point attribute.
func buildGroupDataPoints(gs *pb.ClientGroupedStats, startNs, endNs uint64, defaultService string, otelMode bool) []*otlpmetrics.HistogramDataPoint {
	var pts []*otlpmetrics.HistogramDataPoint

	if len(gs.OkSummary) > 0 {
		if dp := decodeAndBuildDataPoint(gs, gs.OkSummary, startNs, endNs, false, defaultService, otelMode); dp != nil {
			pts = append(pts, dp)
		}
	}
	if len(gs.ErrorSummary) > 0 {
		if dp := decodeAndBuildDataPoint(gs, gs.ErrorSummary, startNs, endNs, true, defaultService, otelMode); dp != nil {
			pts = append(pts, dp)
		}
	}
	return pts
}

func decodeAndBuildDataPoint(gs *pb.ClientGroupedStats, sketchBytes []byte, startNs, endNs uint64, isError bool, defaultService string, otelMode bool) *otlpmetrics.HistogramDataPoint {
	bucketCounts, sum, minSec, maxSec, count, err := sketchToHistogram(sketchBytes, spanMetricBounds)
	if err != nil {
		log.Error("stats_to_otlp_metrics: failed to decode sketch: %v", err)
		return nil
	}
	if count == 0 {
		return nil
	}

	dp := &otlpmetrics.HistogramDataPoint{
		StartTimeUnixNano: startNs,
		TimeUnixNano:      endNs,
		Count:             count,
		Sum:               &sum,
		Min:               &minSec,
		Max:               &maxSec,
		ExplicitBounds:    spanMetricBounds,
		BucketCounts:      bucketCounts,
		Attributes:        buildDataPointAttributes(gs, isError, defaultService, otelMode),
	}
	return dp
}

// buildDataPointAttributes returns the data-point attributes for a ClientGroupedStats group.
// When gs.Service differs from defaultService, service.name is added so the backend can
// distinguish spans from non-default services without a separate resource.
func buildDataPointAttributes(gs *pb.ClientGroupedStats, isError bool, defaultService string, otelMode bool) []*otlpcommon.KeyValue {
	var attrs []*otlpcommon.KeyValue

	// OTel semantic-convention attributes (both modes).
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
	if isError {
		attrs = append(attrs, otlpKeyValue("status.code", otlpStringValue("ERROR")))
	}
	// PeerTags carry additional OTel dimensions (e.g. http.route, rpc.method) as "key:value" pairs.
	for _, tag := range gs.PeerTags {
		if k, v, ok := strings.Cut(tag, ":"); ok && k != "" {
			attrs = append(attrs, otlpKeyValue(k, otlpStringValue(v)))
		}
	}

	// When the span's service differs from the payload default, carry it on the data point.
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
		// A group is top-level only when every span in it was top-level (TopLevelHits == Hits).
		// A mixed group (some top-level, some not) is conservatively reported as non-top-level.
		attrs = append(attrs, otlpKeyValue("datadog.span.top_level", otlpBoolValue(gs.Hits > 0 && gs.TopLevelHits == gs.Hits)))
	}

	return attrs
}

// sketchToHistogram decodes a DDSketch from bytes and projects its values into fixed histogram buckets.
// Sketch values are in nanoseconds; the function converts them to seconds.
// Returns (bucketCounts, sumSec, minSec, maxSec, totalCount, error).
func sketchToHistogram(sketchBytes []byte, bounds []float64) ([]uint64, float64, float64, float64, uint64, error) {
	sketch, err := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
	if err != nil {
		return nil, 0, 0, 0, 0, err
	}
	if err := sketch.DecodeAndMergeWith(sketchBytes); err != nil {
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
		// Find the bucket: first index i where bounds[i] > valueSec.
		// OTLP convention: BucketCounts[i] accumulates values in [bounds[i-1], bounds[i]).
		idx := sort.Search(len(bounds), func(i int) bool { return bounds[i] > valueSec })
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
