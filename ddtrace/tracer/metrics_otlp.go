// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"context"
	"runtime"
	"runtime/debug"
	"strings"

	ddmetric "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/metric"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// otelRuntimeMetrics holds the MeterProvider for OTLP runtime metrics.
type otelRuntimeMetrics struct {
	provider otelmetric.MeterProvider
}

// isOTLPMetricsEnabled returns true when DD_METRICS_OTEL_ENABLED is set to a truthy value.
func isOTLPMetricsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(env.Get("DD_METRICS_OTEL_ENABLED")))
	return v == "true" || v == "1"
}

// startOTLPRuntimeMetrics creates a MeterProvider and registers observable instruments
// for Go runtime metrics using OTel semantic convention names.
//
// OTel Go runtime metrics conventions:
// https://opentelemetry.io/docs/specs/semconv/runtime/go-metrics/
//
// Semantic-core equivalence mappings:
// https://github.com/DataDog/semantic-core/blob/main/sor/domains/metrics/integrations/go/_equivalence/otel_dd.yaml
func startOTLPRuntimeMetrics() (*otelRuntimeMetrics, error) {
	mp, err := ddmetric.NewMeterProvider(
		ddmetric.WithExportInterval(defaultMetricsReportInterval),
	)
	if err != nil {
		return nil, err
	}

	meter := mp.Meter("github.com/DataDog/dd-trace-go/runtime")

	// go.memory.used - Memory used by the Go runtime
	// Attribute: go.memory.type = {stack, other}
	memUsed, err := meter.Float64ObservableGauge("go.memory.used",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Memory used by the Go runtime."),
	)
	if err != nil {
		return nil, err
	}

	// go.memory.limit - GOMEMLIMIT value
	memLimit, err := meter.Int64ObservableGauge("go.memory.limit",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("The maximum amount of memory available to the Go runtime."),
	)
	if err != nil {
		return nil, err
	}

	// go.memory.allocated - Cumulative bytes allocated (monotonic)
	memAllocated, err := meter.Int64ObservableCounter("go.memory.allocated",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Memory allocated to the heap by the application."),
	)
	if err != nil {
		return nil, err
	}

	// go.memory.allocations - Cumulative allocation count (monotonic)
	memAllocations, err := meter.Int64ObservableCounter("go.memory.allocations",
		otelmetric.WithUnit("{allocation}"),
		otelmetric.WithDescription("Count of allocations to the heap by the application."),
	)
	if err != nil {
		return nil, err
	}

	// go.memory.gc.goal - Target heap size for the next GC cycle
	gcGoal, err := meter.Int64ObservableGauge("go.memory.gc.goal",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Heap size target for the end of the GC cycle."),
	)
	if err != nil {
		return nil, err
	}

	// go.goroutine.count - Number of active goroutines
	goroutineCount, err := meter.Int64ObservableGauge("go.goroutine.count",
		otelmetric.WithUnit("{goroutine}"),
		otelmetric.WithDescription("Count of live goroutines."),
	)
	if err != nil {
		return nil, err
	}

	// go.processor.limit - GOMAXPROCS
	processorLimit, err := meter.Int64ObservableGauge("go.processor.limit",
		otelmetric.WithUnit("{thread}"),
		otelmetric.WithDescription("The number of OS threads that can execute user-level Go code simultaneously."),
	)
	if err != nil {
		return nil, err
	}

	// go.config.gogc - GOGC value
	configGogc, err := meter.Int64ObservableGauge("go.config.gogc",
		otelmetric.WithUnit("%"),
		otelmetric.WithDescription("Heap size target percentage at which to trigger a GC cycle."),
	)
	if err != nil {
		return nil, err
	}

	// Attributes for go.memory.used breakdown
	attrStack := otelmetric.WithAttributes(attribute.String("go.memory.type", "stack"))
	attrOther := otelmetric.WithAttributes(attribute.String("go.memory.type", "other"))

	// Register a batch callback for all observable instruments
	var ms runtime.MemStats
	_, err = meter.RegisterCallback(
		func(ctx context.Context, o otelmetric.Observer) error {
			runtime.ReadMemStats(&ms)

			// go.memory.used with go.memory.type attribute
			o.ObserveFloat64(memUsed, float64(ms.StackInuse), attrStack)
			o.ObserveFloat64(memUsed, float64(ms.HeapInuse), attrOther)

			// go.memory.limit: GOMEMLIMIT (returns MaxInt64 if not set)
			o.ObserveInt64(memLimit, debug.SetMemoryLimit(-1))

			// go.memory.allocated: cumulative heap allocation bytes
			o.ObserveInt64(memAllocated, int64(ms.TotalAlloc))

			// go.memory.allocations: cumulative allocation count
			o.ObserveInt64(memAllocations, int64(ms.Mallocs))

			// go.memory.gc.goal: next GC target
			o.ObserveInt64(gcGoal, int64(ms.NextGC))

			// go.goroutine.count
			o.ObserveInt64(goroutineCount, int64(runtime.NumGoroutine()))

			// go.processor.limit: GOMAXPROCS
			o.ObserveInt64(processorLimit, int64(runtime.GOMAXPROCS(0)))

			// go.config.gogc: read GOGC without changing it
			gogc := debug.SetGCPercent(-1)
			debug.SetGCPercent(gogc)
			o.ObserveInt64(configGogc, int64(gogc))

			return nil
		},
		memUsed, memLimit, memAllocated, memAllocations, gcGoal,
		goroutineCount, processorLimit, configGogc,
	)
	if err != nil {
		return nil, err
	}

	log.Debug("Started OTLP runtime metrics with OTel-native naming (go.*)")
	return &otelRuntimeMetrics{provider: mp}, nil
}

// stop shuts down the OTLP runtime metrics pipeline.
func (o *otelRuntimeMetrics) stop() {
	if o == nil || o.provider == nil {
		return
	}
	if err := ddmetric.Shutdown(context.Background(), o.provider); err != nil {
		log.Error("Error shutting down OTLP runtime metrics: %v", err)
	}
}
