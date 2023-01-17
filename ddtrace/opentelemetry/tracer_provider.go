// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentelemetry

import (
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var _ oteltrace.TracerProvider = (*tracerProvider)(nil)

type tracerProvider struct {
	tracer *oteltracer
}

const defaultName = "otel_datadog"

func (t *tracerProvider) Tracer(name string, options ...oteltrace.TracerOption) oteltrace.Tracer {
	if len(name) == 0 {
		log.Warn("provided tracer name is invalid: `%s`, using default value: %s", name, defaultName)
	}
	tracer.Start()
	return &oteltracer{
		name:   name,
		cfg:    oteltrace.NewTracerConfig(options...),
		Tracer: internal.GetGlobalTracer(),
	}
}
