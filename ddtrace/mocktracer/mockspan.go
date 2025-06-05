// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

import (
	"errors"
	"fmt"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

var _ Span = (*MockspanV2Adapter)(nil)

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

	// Links returns the span's span links.
	Links() []ddtrace.SpanLink

	// Events returns the span's span events.
	Events() []SpanEvent

	// Stringer allows pretty-printing the span's fields for debugging.
	fmt.Stringer

	Integration() string
}

// SpanEvent represents a span event from a mockspan.
type SpanEvent = v2.SpanEvent

type MockspanV2Adapter struct {
	Span *v2.Span
}

// BaggageItem implements ddtrace.Span.
func (msa MockspanV2Adapter) BaggageItem(key string) string {
	return msa.Span.Unwrap().BaggageItem(key)
}

// Finish implements ddtrace.Span.
func (msa MockspanV2Adapter) Finish(opts ...ddtrace.FinishOption) {
	t := internal.GetGlobalTracer().(internal.TracerV2Adapter)
	sp := msa.Span.Unwrap()
	t.Tracer.(v2.Tracer).FinishSpan(sp)
	sp.Finish(internal.ApplyV1FinishOptions(opts...))
}

// SetBaggageItem implements ddtrace.Span.
func (msa MockspanV2Adapter) SetBaggageItem(key string, val string) {
	msa.Span.Unwrap().SetBaggageItem(key, val)
}

// SetOperationName implements ddtrace.Span.
func (msa MockspanV2Adapter) SetOperationName(operationName string) {
	msa.Span.Unwrap().SetOperationName(operationName)
}

// SetTag implements ddtrace.Span.
func (msa MockspanV2Adapter) SetTag(key string, value interface{}) {
	msa.Span.SetTag(key, value)
}

// Context implements Span.
func (msa MockspanV2Adapter) Context() ddtrace.SpanContext {
	return internal.SpanContextV2Adapter{Ctx: msa.Span.Context()}
}

// FinishTime implements Span.
func (msa MockspanV2Adapter) FinishTime() time.Time {
	return msa.Span.FinishTime()
}

// OperationName implements Span.
func (msa MockspanV2Adapter) OperationName() string {
	return msa.Span.OperationName()
}

// ParentID implements Span.
func (msa MockspanV2Adapter) ParentID() uint64 {
	return msa.Span.ParentID()
}

// SpanID implements Span.
func (msa MockspanV2Adapter) SpanID() uint64 {
	return msa.Span.SpanID()
}

// StartTime implements Span.
func (msa MockspanV2Adapter) StartTime() time.Time {
	return msa.Span.StartTime()
}

// String implements Span.
func (msa MockspanV2Adapter) String() string {
	return msa.Span.String()
}

// Tag implements Span.
func (msa MockspanV2Adapter) Tag(k string) interface{} {
	switch k {
	case ext.Error:
		v := msa.Span.Tag(ext.ErrorMsg)
		if v == nil {
			return nil
		}
		err := errors.New(v.(string))
		return err
	case ext.HTTPCode,
		ext.MessagingKafkaPartition,
		ext.NetworkDestinationPort,
		ext.RedisDatabaseIndex:
		v := msa.Span.Tag(k)
		if v == nil {
			return nil
		}
		switch v := v.(type) {
		case float64:
			return int(v)
		default:
			return v
		}
	case ext.SamplingPriority:
		v := msa.Span.Tag("_sampling_priority_v1")
		if v == nil {
			return 0
		}
		return int(v.(float64))
	case ext.ErrorStack:
		switch v := msa.Span.Tag(k); v {
		case nil:
			fallthrough
		case "":
			// If ext.ErrorStack is not set, but ext.Error is, then we can assume that the
			// stack trace is disabled.
			if msa.Span.Tag(ext.ErrorMsg) != nil {
				return "<debug stack disabled>"
			}

			// Otherwise, we can assume that the error is not set.
			return nil
		default:
			return v
		}
	}

	return msa.Span.Tag(k)
}

// Tags implements Span.
func (msa MockspanV2Adapter) Tags() map[string]interface{} {
	tags := msa.Span.Tags()
	var hasError bool
	if _, hasError = tags[ext.ErrorMsg]; hasError {
		tags[ext.Error] = errors.New(tags[ext.ErrorMsg].(string))
	}
	es, ok := tags[ext.ErrorStack]
	if !ok && hasError {
		tags[ext.ErrorStack] = "<debug stack disabled>"
	}
	if v, ok := es.(string); ok && v == "" && hasError {
		tags[ext.ErrorStack] = "<debug stack disabled>"
	}
	return tags
}

// TraceID implements Span.
func (msa MockspanV2Adapter) TraceID() uint64 {
	return msa.Span.TraceID()
}

// Links returns the span's span links.
func (msa MockspanV2Adapter) Links() []ddtrace.SpanLink {
	return msa.Span.Links()
}

// Events returns the span's span events.
func (msa MockspanV2Adapter) Events() []SpanEvent {
	return msa.Span.Events()
}

// Integration returns the component from which the mockspan was created.
func (msa MockspanV2Adapter) Integration() string {
	return msa.Span.Integration()
}
