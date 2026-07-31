// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate go run github.com/tinylib/msgp -o event_msgp.go -tests=false

package stacktrace

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal"

	"github.com/tinylib/msgp/msgp"
)

var _ msgp.Marshaler = (*Event)(nil)

type EventCategory string

const (
	// ExceptionEvent is the event type for exception events
	ExceptionEvent EventCategory = "exception"
	// VulnerabilityEvent is the event type for vulnerability events
	VulnerabilityEvent EventCategory = "vulnerability"
	// ExploitEvent is the event type for exploit events
	ExploitEvent EventCategory = "exploit"
)

const SpanKey = "_dd.stack"

// Event is the toplevel structure to contain a stacktrace and the additional information needed to correlate it with other data
type Event struct {
	// Category is a well-known type of the event, not optional
	Category EventCategory `msg:"-"`
	// Type is a value event category specific, optional
	Type string `msg:"type,omitempty"`
	// Language is the language of the code that generated the event (set to "go" anyway here)
	Language string `msg:"language,omitempty"`
	// ID is the id of the event, optional for exceptions but mandatory for vulnerabilities and exploits to correlate with more data
	ID string `msg:"id,omitempty"`
	// Message is a generic message for the event
	Message string `msg:"message,omitempty"`
	// Frames is the stack trace of the event
	Frames StackTrace `msg:"frames"`
}

// NewEvent creates a new stacktrace event with the given category and options.
func NewEvent(eventCat EventCategory, options ...Option) *Event {
	event := &Event{
		Category: eventCat,
		Language: "go",
	}
	cfg := eventConfig{
		event: event,
		depth: defaultMaxDepth,
	}
	for _, opt := range options {
		if opt != nil {
			opt(&cfg)
		}
	}
	event.Frames = SkipAndCaptureWithDepth(cfg.depth, cfg.skip+1)
	return event
}

type eventConfig struct {
	event *Event
	skip  int
	depth int
}

// Option configures a stacktrace event.
type Option func(*eventConfig)

// WithType sets the type of the event.
func WithType(eventType string) Option {
	return func(cfg *eventConfig) {
		cfg.event.Type = eventType
	}
}

// WithMessage sets the message of the event.
func WithMessage(message string) Option {
	return func(cfg *eventConfig) {
		cfg.event.Message = message
	}
}

// WithID sets the ID of the event.
func WithID(id string) Option {
	return func(cfg *eventConfig) {
		cfg.event.ID = id
	}
}

// WithSkip sets the number of caller frames to skip after the stacktrace
// capture machinery. Negative values are treated as zero.
func WithSkip(skip int) Option {
	return func(cfg *eventConfig) {
		cfg.skip = max(0, skip)
	}
}

// WithAdditionalSkip adds the given number of frames to skip to the existing
// skip value. Negative values are treated as zero.
func WithAdditionalSkip(skip int) Option {
	return func(cfg *eventConfig) {
		if skip <= 0 {
			return
		}
		cfg.skip += skip
	}
}

// WithDepth sets the maximum number of frames to capture. A non-positive depth
// uses the default depth.
func WithDepth(depth int) Option {
	return func(cfg *eventConfig) {
		cfg.depth = depth
	}
}

// GetSpanValue returns the value to be set as a tag on a span for the given stacktrace events.
func GetSpanValue(events ...*Event) any {
	return getSpanValue(events...)
}

// AddToSpan submits the events to the given span as a tag. It returns true when
// at least one event was submitted; TagSetter does not report whether it stored
// the value.
func AddToSpan(span trace.TagSetter, events ...*Event) bool {
	if len(events) == 0 {
		return false
	}
	value := getSpanValue(events...)
	span.SetTag(SpanKey, value)
	return true
}

// MergeSpanValues combines two _dd.stack meta_struct values. The caller must
// synchronize access to current.
func MergeSpanValues(current, next any) (any, error) {
	currentEvents, ok := current.(map[string][]*Event)
	if !ok {
		return nil, fmt.Errorf("current span value has type %T, expected map[string][]*stacktrace.Event", current)
	}
	nextEvents, ok := next.(map[string][]*Event)
	if !ok {
		return nil, fmt.Errorf("next span value has type %T, expected map[string][]*stacktrace.Event", next)
	}
	for category, events := range nextEvents {
		currentEvents[category] = append(currentEvents[category], events...)
	}
	return currentEvents, nil
}

func getSpanValue(events ...*Event) internal.MetaStructValue {
	groupByCategory := make(map[string][]*Event, 3)
	for _, event := range events {
		if _, ok := groupByCategory[string(event.Category)]; !ok {
			groupByCategory[string(event.Category)] = []*Event{event}
			continue
		}
		groupByCategory[string(event.Category)] = append(groupByCategory[string(event.Category)], event)
	}
	return internal.MetaStructValue{Value: groupByCategory}
}
