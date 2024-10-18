// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestNewEvent(t *testing.T) {
	event := NewEvent(ExceptionEvent, WithMessage("message"), WithType("type"), WithID("id"))
	require.Equal(t, ExceptionEvent, event.Category)
	require.Equal(t, "go", event.Language)
	require.Equal(t, "message", event.Message)
	require.Equal(t, "type", event.Type)
	require.Equal(t, "id", event.ID)
	require.GreaterOrEqual(t, len(event.Frames), 2)
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

func TestMsgPackSerialization(t *testing.T) {
	event := NewEvent(ExceptionEvent, WithMessage("message"), WithType("type"), WithID("id"))
	spanValue := GetSpanValue(event)

	eventsMap := spanValue.(internal.MetaStructValue).Value

	_, err := msgp.AppendIntf(nil, eventsMap)
	require.NoError(t, err)
}
