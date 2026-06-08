// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package tracing provides span helpers shared between the aerospike contrib
// wrapper and the Orchestrion advice template. It intentionally does not import
// aerospike-client-go so that the orchestrion path does not drag the SDK into
// instrumented binaries via the injected declarations.
package tracing

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Span is a type alias so Orchestrion templates only need to import this
// package — no separate tracer import required in the advice.
type Span = tracer.Span

const ComponentName = "aerospike/aerospike-client-go.v7"

var Instr *instrumentation.Instrumentation

func init() {
	Instr = instrumentation.Load(instrumentation.PackageAerospikeClientGoV7)
}

// StartSpan starts a new aerospike span from ctx using explicit service/op
// names. Used by the contrib wrapper, which may override defaults via options.
func StartSpan(ctx context.Context, serviceName, serviceSource, operationName, resourceName string) *Span {
	span, _ := tracer.StartSpanFromContext(ctx, operationName,
		tracer.SpanType(ext.SpanTypeAerospike),
		instrumentation.ServiceNameWithSource(serviceName, serviceSource),
		tracer.ResourceName(resourceName),
		tracer.Tag(ext.Component, ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemAerospike),
	)
	return span
}

// StartDefaultSpan starts a span from context.Background() using the service
// and operation names resolved from the instrumentation registry. Used by the
// Orchestrion advice template where no client config is in scope.
func StartDefaultSpan(resourceName string) *Span {
	return StartSpan(
		context.Background(),
		Instr.ServiceName(instrumentation.ComponentDefault, nil),
		string(instrumentation.PackageAerospikeClientGoV7),
		Instr.OperationName(instrumentation.ComponentDefault, nil),
		resourceName,
	)
}

// FinishSpan finishes span, tagging it with err if non-nil.
func FinishSpan(span *Span, err error) {
	span.Finish(tracer.WithError(err))
}
