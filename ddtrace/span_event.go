// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ddtrace

import (
	"time"
)

// SpanEvent represent an event at an instant in time related to this span, but not necessarily during the span.
type SpanEvent struct {
	// Name is the name of event.
	Name string

	// Time is the time when the event happened.
	Time time.Time

	// Attributes is a map of string to attribute.
	// Only the following types are supported:
	//   string, integer (any), boolean, float (any), []string, []integer (any), []boolean, []float (any)
	Attributes map[string]any
}

// NewSpanEvent creates a new span event with the given name and attributes.
func NewSpanEvent(name string, opts ...SpanEventOption) SpanEvent {
	evt := SpanEvent{
		Name: name,
		Time: time.Now(),
	}
	for _, opt := range opts {
		opt(&evt)
	}
	return evt
}

// SpanEventOption can be used to customize an event created with NewSpanEvent.
type SpanEventOption func(evt *SpanEvent)

// WithSpanEventTimestamp sets the time when the span event occurred.
func WithSpanEventTimestamp(tStamp time.Time) SpanEventOption {
	return func(evt *SpanEvent) {
		evt.Time = tStamp
	}
}

// WithSpanEventAttributes sets the given attributes for the span event.
func WithSpanEventAttributes(attributes map[string]any) SpanEventOption {
	return func(evt *SpanEvent) {
		evt.Attributes = attributes
	}
}
