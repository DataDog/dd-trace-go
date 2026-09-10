// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"math"
	goruntime "runtime/metrics"

	ddversion "github.com/DataDog/dd-trace-go/v2/internal/version"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

const otelRuntimeMetricsInstrumentationScope = "go.runtime"

func startGoRuntimeMetrics(ctx context.Context) error {
	meter := otel.GetMeterProvider().Meter(
		otelRuntimeMetricsInstrumentationScope,
		otelmetric.WithInstrumentationVersion(ddversion.Tag),
	)
	return registerRecommendedMetrics(ctx, meter)
}

func registerRecommendedMetrics(_ context.Context, meter otelmetric.Meter) error {
	samples := []goruntime.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
		{Name: "/gc/gomemlimit:bytes"},
		{Name: "/gc/heap/allocs:bytes"},
		{Name: "/gc/heap/allocs:objects"},
		{Name: "/gc/heap/goal:bytes"},
		{Name: "/sched/goroutines:goroutines"},
		{Name: "/sched/gomaxprocs:threads"},
		{Name: "/gc/gogc:percent"},
	}

	typeOther := otelmetric.WithAttributes(memoryTypeAttr("other"))
	typeStack := otelmetric.WithAttributes(memoryTypeAttr("stack"))

	memUsed, err := meter.Int64ObservableUpDownCounter(
		"go.memory.used",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Memory used by the Go runtime."),
	)
	if err != nil {
		return err
	}

	memLimit, err := meter.Int64ObservableUpDownCounter(
		"go.memory.limit",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Go runtime memory limit configured by the user, if a limit exists."),
	)
	if err != nil {
		return err
	}

	memAllocated, err := meter.Int64ObservableCounter(
		"go.memory.allocated",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Memory allocated to the heap by the application."),
	)
	if err != nil {
		return err
	}

	memAllocations, err := meter.Int64ObservableCounter(
		"go.memory.allocations",
		otelmetric.WithUnit("{allocation}"),
		otelmetric.WithDescription("Count of allocations to the heap by the application."),
	)
	if err != nil {
		return err
	}

	gcGoal, err := meter.Int64ObservableUpDownCounter(
		"go.memory.gc.goal",
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Heap size target for the end of the next garbage collection cycle."),
	)
	if err != nil {
		return err
	}

	goroutineCount, err := meter.Int64ObservableUpDownCounter(
		"go.goroutine.count",
		otelmetric.WithUnit("{goroutine}"),
		otelmetric.WithDescription("Count of live goroutines."),
	)
	if err != nil {
		return err
	}

	processorLimit, err := meter.Int64ObservableUpDownCounter(
		"go.processor.limit",
		otelmetric.WithUnit("{thread}"),
		otelmetric.WithDescription("The number of OS threads that can execute user-level Go code simultaneously."),
	)
	if err != nil {
		return err
	}

	configGOGC, err := meter.Int64ObservableUpDownCounter(
		"go.config.gogc",
		otelmetric.WithUnit("%"),
		otelmetric.WithDescription("Heap size target percentage configured by the user, otherwise 100."),
	)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(
		func(_ context.Context, o otelmetric.Observer) error {
			goruntime.Read(samples)

			total := int64(samples[0].Value.Uint64())
			stack := int64(samples[1].Value.Uint64())
			released := int64(samples[2].Value.Uint64())
			o.ObserveInt64(memUsed, total-released-stack, typeOther)
			o.ObserveInt64(memUsed, stack, typeStack)
			if limit := int64(samples[3].Value.Uint64()); limit != math.MaxInt64 {
				o.ObserveInt64(memLimit, limit)
			}
			o.ObserveInt64(memAllocated, int64(samples[4].Value.Uint64()))
			o.ObserveInt64(memAllocations, int64(samples[5].Value.Uint64()))
			o.ObserveInt64(gcGoal, int64(samples[6].Value.Uint64()))
			o.ObserveInt64(goroutineCount, int64(samples[7].Value.Uint64()))
			o.ObserveInt64(processorLimit, int64(samples[8].Value.Uint64()))
			o.ObserveInt64(configGOGC, int64(samples[9].Value.Uint64()))
			return nil
		},
		memUsed, memLimit, memAllocated, memAllocations,
		gcGoal, goroutineCount, processorLimit, configGOGC,
	)
	return err
}

func memoryTypeAttr(v string) attribute.KeyValue {
	return attribute.String("go.memory.type", v)
}
