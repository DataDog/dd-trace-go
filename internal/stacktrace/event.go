// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate msgp -o event_msgp.go -tests=false

package stacktrace

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"

	"github.com/google/uuid"
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

// NewEvent creates a new stacktrace event with the given category, type and message
func NewEvent(eventCat EventCategory, eventType, message string) *Event {
	return &Event{
		Category: eventCat,
		Type:     eventType,
		Language: "go",
		Message:  message,
		Frames:   TakeWithSkip(defaultCallerSkip + 1),
	}
}

// IDLink returns a UUID to link the stacktrace event with other data
func (e *Event) IDLink() string {
	if e.ID != "" {
		newUUID, err := uuid.NewUUID()
		if err != nil {
			return ""
		}

		e.ID = newUUID.String()
	}

	return e.ID
}

// Enabled returns whether the stacktrace is enabled
func Enabled() bool {
	return enabled
}

// AddToSpan adds the event to the given span's root span as a tag
func AddToSpan(span ddtrace.Span, events ...*Event) {

	groupByCategory := map[EventCategory][]*Event{
		ExceptionEvent:     {},
		VulnerabilityEvent: {},
		ExploitEvent:       {},
	}

	for _, event := range events {
		groupByCategory[event.Category] = append(groupByCategory[event.Category], event)
	}

	type rooter interface {
		Root() ddtrace.Span
	}
	if lrs, ok := span.(rooter); ok {
		span = lrs.Root()
	}

	span.SetTag("_dd.stack", internal.MetaStructValue{Value: groupByCategory})
}
