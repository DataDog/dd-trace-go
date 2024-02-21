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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
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

func tagName(eventCategory EventCategory) string {
	return fmt.Sprintf("_dd.stack.%s", eventCategory)
}

func newEvent(eventCat EventCategory, message string) *Event {
	return &Event{
		Category: eventCat,
		Language: "go",
		Message:  message,
		Frames:   TakeWithSkip(defaultCallerSkip + 1),
	}
}

func NewException(message string) *Event {
	return newEvent(ExceptionEvent, message)
}

func NewVulnerability(message string) *Event {
	return newEvent(VulnerabilityEvent, message)
}

func NewExploit(message string) *Event {
	return newEvent(ExploitEvent, message)
}

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

func (e *Event) AddToSpan(span ddtrace.Span) {
	span.SetTag(tagName(e.Category), e)
}
