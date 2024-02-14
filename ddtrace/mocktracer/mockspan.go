// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

import (
	"fmt"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

var _ Span = (*mockspanV2Adapter)(nil)

// Span is an interface that allows querying a span returned by the mock tracer.
type Span interface {
	// SpanID returns the span's ID.
	SpanID() uint64

	// TraceID returns the span's trace ID.
	TraceID() uint64

	// ParentID returns the span's parent ID.
	ParentID() uint64

	// StartTime returns the time when the span has started.
	StartTime() time.Time

	// FinishTime returns the time when the span has finished.
	FinishTime() time.Time

	// OperationName returns the operation name held by this span.
	OperationName() string

	// Tag returns the value of the tag at key k.
	Tag(k string) interface{}

	// Tags returns a copy of all the tags in this span.
	Tags() map[string]interface{}

	// Context returns the span's SpanContext.
	Context() ddtrace.SpanContext

	// Stringer allows pretty-printing the span's fields for debugging.
	fmt.Stringer
}

type mockspanV2Adapter struct {
	span *v2.Span
}

// Context implements Span.
func (msa mockspanV2Adapter) Context() ddtrace.SpanContext {
	return internal.SpanContextV2Adapter{Ctx: msa.span.Context()}
}

// FinishTime implements Span.
func (msa mockspanV2Adapter) FinishTime() time.Time {
	return msa.span.FinishTime()
}

// OperationName implements Span.
func (msa mockspanV2Adapter) OperationName() string {
	return msa.span.OperationName()
}

// ParentID implements Span.
func (msa mockspanV2Adapter) ParentID() uint64 {
	return msa.span.ParentID()
}

// SpanID implements Span.
func (msa mockspanV2Adapter) SpanID() uint64 {
	return msa.span.SpanID()
}

// StartTime implements Span.
func (msa mockspanV2Adapter) StartTime() time.Time {
	return msa.span.StartTime()
}

// String implements Span.
func (msa mockspanV2Adapter) String() string {
	return msa.span.String()
}

// Tag implements Span.
func (msa mockspanV2Adapter) Tag(k string) interface{} {
	return msa.span.Tag(k)
}

// Tags implements Span.
func (msa mockspanV2Adapter) Tags() map[string]interface{} {
	return msa.span.Tags()
}

// TraceID implements Span.
func (msa mockspanV2Adapter) TraceID() uint64 {
	return msa.span.TraceID()
}
