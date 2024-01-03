// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package ddtrace contains the interfaces that specify the implementations of Datadog's
// tracing library, as well as a set of sub-packages containing various implementations:
// our native implementation ("tracer") and a mock tracer to be used for testing ("mocktracer").
// Additionally, package "ext" provides a set of tag names and values specific to Datadog's APM product.
//
// To get started, visit the documentation for any of the packages you'd like to begin
// with by accessing the subdirectories of this package: https://godoc.org/github.com/DataDog/dd-trace-go/v2/ddtrace#pkg-subdirectories.
package ddtrace // import "github.com/DataDog/dd-trace-go/v2/ddtrace"

import (
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// // SpanContextW3C represents a SpanContext with an additional method to allow
// // access of the 128-bit trace id of the span, if present.
// type SpanContextW3C interface {
// 	SpanContext

// 	// TraceID128 returns the hex-encoded 128-bit trace ID that this context is carrying.
// 	// The string will be exactly 32 bytes and may include leading zeroes.
// 	TraceID128() string

// 	// TraceID128 returns the raw bytes of the 128-bit trace ID that this context is carrying.
// 	TraceID128Bytes() [16]byte
// }

// SpanContext represents a span state that can propagate to descendant spans
// and across process boundaries. It contains all the information needed to
// spawn a direct descendant of the span that it belongs to. It can be used
// to create distributed tracing by propagating it using the provided interfaces.
type SpanContext interface {
	// SpanID returns the span ID that this context is carrying.
	SpanID() uint64

	// TraceID returns the trace ID that this context is carrying.
	TraceID() string

	// TraceID128 returns the raw bytes of the 128-bit trace ID that this context is carrying.
	TraceIDBytes() [16]byte

	// ForeachBaggageItem provides an iterator over the key/value pairs set as
	// baggage within this context. Iteration stops when the handler returns
	// false.
	ForeachBaggageItem(handler func(k, v string) bool)
}

// FinishOption is a configuration option that can be used with a Span's Finish method.
type FinishOption func(cfg *FinishConfig)

// FinishConfig holds the configuration for finishing a span. It is usually passed around by
// reference to one or more FinishOption functions which shape it into its final form.
type FinishConfig struct {
	// FinishTime represents the time that should be set as finishing time for the
	// span. Implementations should use the current time when FinishTime.IsZero().
	FinishTime time.Time

	// Error holds an optional error that should be set on the span before
	// finishing.
	Error error

	// NoDebugStack will prevent any set errors from generating an attached stack trace tag.
	NoDebugStack bool

	// StackFrames specifies the number of stack frames to be attached in spans that finish with errors.
	StackFrames uint

	// SkipStackFrames specifies the offset at which to start reporting stack frames from the stack.
	SkipStackFrames uint
}

// Logger implementations are able to log given messages that the tracer or profiler might output.
type Logger interface {
	// Log prints the given message.
	Log(msg string)
}

// UseLogger sets l as the logger for all tracer and profiler logs.
func UseLogger(l Logger) {
	log.UseLogger(l)
}
