// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal"
)

func TestNewEvent(t *testing.T) {
	event := NewEvent(
		ExceptionEvent,
		nil,
		WithMessage("first"),
		WithMessage("message"),
		WithType("type"),
		WithID("id"),
		WithSkip(-1),
		WithDepth(0),
	)
	require.Equal(t, ExceptionEvent, event.Category)
	require.Equal(t, "go", event.Language)
	require.Equal(t, "message", event.Message)
	require.Equal(t, "type", event.Type)
	require.Equal(t, "id", event.ID)
	for _, frame := range event.Frames {
		require.NotContains(t, frame.Namespace, "github.com/DataDog/dd-trace-go")
	}
}

func TestNewEventWithSkipOption(t *testing.T) {
	event := NewEvent(ExploitEvent, WithSkip(1_000), WithMessage("message"), WithID("id"))
	require.Equal(t, ExploitEvent, event.Category)
	require.Equal(t, "go", event.Language)
	require.Equal(t, "message", event.Message)
	require.Equal(t, "id", event.ID)
	require.Empty(t, event.Frames)
}

func TestNewEventWithDepthOption(t *testing.T) {
	event := NewEvent(ExploitEvent, WithDepth(1), WithID("id"))
	require.Equal(t, ExploitEvent, event.Category)
	require.Equal(t, "id", event.ID)
	require.Len(t, event.Frames, 1)
}

func TestNewEventWithNegativeDepthUsesDefault(t *testing.T) {
	event := NewEvent(ExploitEvent, WithDepth(-1))
	require.NotEmpty(t, event.Frames)
	require.LessOrEqual(t, len(event.Frames), defaultMaxDepth)
}

func TestEventToSpan(t *testing.T) {
	event1 := NewEvent(ExceptionEvent, WithMessage("message1"))
	event2 := NewEvent(ExploitEvent, WithMessage("message2"))
	spanValue := GetSpanValue(event1, event2)

	eventsMap := spanValue.(internal.MetaStructValue).Value.(map[string][]*Event)
	require.Len(t, eventsMap, 2)

	eventsCat := eventsMap[string(ExceptionEvent)]
	require.Len(t, eventsCat, 1)

	require.Equal(t, *event1, *eventsCat[0])

	eventsCat = eventsMap[string(ExploitEvent)]
	require.Len(t, eventsCat, 1)

	require.Equal(t, *event2, *eventsCat[0])
}

func TestAddToSpan(t *testing.T) {
	event := NewEvent(VulnerabilityEvent, WithID("id"))
	span := trace.TestTagSetter{}
	AddToSpan(span)
	require.Empty(t, span)

	AddToSpan(span, event)
	value, ok := span[SpanKey].(internal.MetaStructValue)
	require.True(t, ok)
	require.Equal(t, map[string][]*Event{"vulnerability": {event}}, value.Value)
}

func TestMergeSpanValues(t *testing.T) {
	existing := NewEvent(VulnerabilityEvent, WithID("existing"))
	additional := NewEvent(VulnerabilityEvent, WithID("additional"))
	exploit := NewEvent(ExploitEvent, WithID("exploit"))
	current := getSpanValue(existing).Value
	next := getSpanValue(additional, exploit).Value

	merged, err := MergeSpanValues(current, next)
	require.NoError(t, err)
	require.Equal(t, map[string][]*Event{
		"vulnerability": {existing, additional},
		"exploit":       {exploit},
	}, merged)

	_, err = MergeSpanValues("invalid", next)
	require.EqualError(t, err, "current span value has type string, expected map[string][]*stacktrace.Event")
	_, err = MergeSpanValues(current, "invalid")
	require.EqualError(t, err, "next span value has type string, expected map[string][]*stacktrace.Event")
}

func TestMsgPackSerialization(t *testing.T) {
	event := NewEvent(ExceptionEvent, WithMessage("message"), WithType("type"), WithID("id"))
	spanValue := GetSpanValue(event)

	eventsMap := spanValue.(internal.MetaStructValue).Value

	_, err := msgp.AppendIntf(nil, eventsMap)
	require.NoError(t, err)
}
