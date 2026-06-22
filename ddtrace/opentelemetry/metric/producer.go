// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"errors"
	"math"
	goruntime "runtime/metrics"
	"sync"
	"time"

	ddversion "github.com/DataDog/dd-trace-go/v2/internal/version"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	goSchedLatencies               = "/sched/latencies:seconds"
	runtimeProducerInstrumentScope = "go.runtime"
)

// RuntimeProducer implements sdkmetric.Producer to emit go.schedule.duration.
// OTel async instruments (e.g. Int64ObservableGauge) cannot produce Histogram
// data points; a Producer injects the pre-aggregated histogram from Go's
// runtime/metrics directly into the collection pipeline.
//
// Pattern mirrors go.opentelemetry.io/contrib/instrumentation/runtime.NewProducer.
type RuntimeProducer struct {
	lock      sync.Mutex
	startTime time.Time
}

var _ sdkmetric.Producer = (*RuntimeProducer)(nil)

// NewRuntimeProducer returns a Producer that emits go.schedule.duration. Register
// it on a reader via sdkmetric.WithProducer when constructing your own
// MeterProvider, or rely on InstallGlobal which auto-registers it.
func NewRuntimeProducer() *RuntimeProducer {
	return &RuntimeProducer{startTime: time.Now()}
}

// Produce returns the current go.schedule.duration histogram, translated from
// runtime/metrics into a cumulative OTel histogram data point.
func (p *RuntimeProducer) Produce(_ context.Context) ([]metricdata.ScopeMetrics, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	samples := []goruntime.Sample{{Name: goSchedLatencies}}
	goruntime.Read(samples)
	hist := samples[0].Value.Float64Histogram()
	now := time.Now()

	dps := convertRuntimeHistogram(hist, p.startTime, now)
	if len(dps) == 0 {
		return nil, errors.New("unable to obtain go.schedule.duration metric from the runtime")
	}

	return []metricdata.ScopeMetrics{
		{
			Scope: instrumentation.Scope{
				Name:    runtimeProducerInstrumentScope,
				Version: ddversion.Tag,
			},
			Metrics: []metricdata.Metrics{
				{
					Name:        "go.schedule.duration",
					Description: "The time goroutines have spent in the scheduler in a runnable state before actually running.",
					Unit:        "s",
					Data: metricdata.Histogram[float64]{
						Temporality: metricdata.CumulativeTemporality,
						DataPoints:  dps,
					},
				},
			},
		},
	}, nil
}

var emptyAttrSet = attribute.EmptySet()

// convertRuntimeHistogram adapts a runtime/metrics Float64Histogram (cumulative
// bucket counts with explicit boundaries) into an OTel HistogramDataPoint.
//
// The translation rules follow upstream go.opentelemetry.io/contrib/instrumentation/runtime:
//   - Drop the first bucket boundary; runtime/metrics encodes it as a lower bound,
//     OTel histogram boundaries are upper bounds only.
//   - Drop a trailing +Inf boundary; OTel's overflow bucket is implicit.
//   - Compute sum as Σ(boundary[i-1] * count_in_bucket_i). This matches the
//     prometheus client_golang convention; it underestimates the true sum
//     because it places each observation at its bucket's lower bound.
func convertRuntimeHistogram(hist *goruntime.Float64Histogram, start, now time.Time) []metricdata.HistogramDataPoint[float64] {
	if hist == nil {
		return nil
	}
	bounds := hist.Buckets
	counts := hist.Counts
	if len(bounds) < 2 {
		return nil
	}
	bounds = bounds[1:]
	if bounds[len(bounds)-1] == math.Inf(1) {
		bounds = bounds[:len(bounds)-1]
	} else {
		counts = append(counts, 0)
	}
	var (
		count uint64
		sum   float64
	)
	for i, c := range counts {
		count += c
		if i > 0 && c != 0 {
			sum += bounds[i-1] * float64(c)
		}
	}
	return []metricdata.HistogramDataPoint[float64]{
		{
			StartTime:    start,
			Time:         now,
			Count:        count,
			Sum:          sum,
			Bounds:       bounds,
			BucketCounts: counts,
			Attributes:   *emptyAttrSet,
		},
	}
}
