// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate msgp -o event_msgp.go -tests=false

package stacktrace

import (
	"fmt"
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

// TagName returns the tag name for the event
func (e *Event) TagName() string {
	return fmt.Sprintf("_dd.stack.%s", e.Category)
}

// NewEvent creates a new stacktrace event with the given category, type and message
func NewEvent(eventCat EventCategory, type_, message string) *Event {
	return &Event{
		Category: eventCat,
		Type:     type_,
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

// AddToSpan uses (*Event).TagName to add the event to a span using span.SetTag
func (e *Event) AddToSpan(span interface{ SetTag(key string, value any) }) {
	span.SetTag(e.TagName(), *e)
}
