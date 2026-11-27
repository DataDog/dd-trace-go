// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package metric

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/internal/otlpjson"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var errShutdown = errors.New("OTLP/JSON metrics exporter is shut down")

// jsonExporter is the metric.Exporter used when the metrics protocol is "http/json".
// The upstream otlpmetrichttp exporter only produces protobuf, so this one encodes
// the SDK metric data to OTLP/JSON itself (see package otlpjson).
type jsonExporter struct {
	sender      *otlpjson.Sender
	temporality metric.TemporalitySelector
	aggregation metric.AggregationSelector
	stopped     atomic.Bool
}

var _ metric.Exporter = (*jsonExporter)(nil)

// newJSONExporter creates an OTLP/JSON metrics exporter. temporality is the
// caller's selector, or the Datadog delta default when nil.
func newJSONExporter(temporality metric.TemporalitySelector) *jsonExporter {
	cfg := internalconfig.Get()
	if temporality == nil {
		temporality = deltaTemporalitySelector()
	}
	return &jsonExporter{
		temporality: temporality,
		aggregation: histogramAggregationSelector(cfg.RuntimeMetricsHistogramAggregation()),
		sender: otlpjson.NewSender(otlpjson.SenderConfig{
			URL:           cfg.RuntimeMetricsHTTPURL(),
			Headers:       cfg.RuntimeMetricsHeaders(),
			Timeout:       exporterTimeout,
			Gzip:          cfg.RuntimeMetricsCompression() == "gzip",
			Signal:        "metrics",
			RejectedField: "rejectedDataPoints",
		}),
	}
}

// Temporality implements metric.Exporter.
func (e *jsonExporter) Temporality(k metric.InstrumentKind) metricdata.Temporality {
	return e.temporality(k)
}

// histogramAggregationSelector maps OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION
// to an aggregation selector, as the upstream otlpmetrichttp exporter does.
// Unknown values fall back to explicit_bucket_histogram.
func histogramAggregationSelector(preference string) metric.AggregationSelector {
	switch preference {
	case "explicit_bucket_histogram":
	case "base2_exponential_bucket_histogram":
		return func(k metric.InstrumentKind) metric.Aggregation {
			if k == metric.InstrumentKindHistogram {
				return metric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 20}
			}
			return metric.DefaultAggregationSelector(k)
		}
	default:
		log.Warn("Unsupported OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION %q; must be explicit_bucket_histogram or base2_exponential_bucket_histogram. Falling back to explicit_bucket_histogram.", preference) //errtrack:ignore invalid user configuration
	}
	return metric.DefaultAggregationSelector
}

// Aggregation implements metric.Exporter.
func (e *jsonExporter) Aggregation(k metric.InstrumentKind) metric.Aggregation {
	return e.aggregation(k)
}

// Export implements metric.Exporter.
func (e *jsonExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if e.stopped.Load() {
		return errShutdown
	}
	buf, release := e.sender.Buffer()
	defer release()
	// A metric that cannot be encoded is dropped from the request; the rest is still sent.
	var encodeErr error
	*buf, encodeErr = otlpjson.EncodeMetrics(*buf, rm)
	if err := e.sender.Send(ctx, *buf); err != nil {
		return err
	}
	return encodeErr
}

// ForceFlush implements metric.Exporter. The exporter keeps no state to flush.
func (e *jsonExporter) ForceFlush(context.Context) error { return nil }

// Shutdown implements metric.Exporter. Later Export calls return errShutdown.
func (e *jsonExporter) Shutdown(context.Context) error {
	e.stopped.Store(true)
	e.sender.Close()
	return nil
}
