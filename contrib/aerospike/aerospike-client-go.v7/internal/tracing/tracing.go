// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package tracing provides span helpers used by the aerospike contrib wrapper.
package tracing

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Span is a type alias used as the return type of StartSpan.
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

// StartDefaultSpan starts a span from ctx, resolving the service and operation
// names at call time. Resolution is deliberately not cached: the service name
// comes from global tracer configuration that tracer.Start can still change
// after a client has been created.
func StartDefaultSpan(ctx context.Context, resourceName string) *Span {
	return StartSpan(
		ctx,
		Instr.ServiceName(instrumentation.ComponentDefault, nil),
		string(instrumentation.PackageAerospikeClientGoV7),
		Instr.OperationName(instrumentation.ComponentDefault, nil),
		resourceName,
	)
}
