// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"

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
	mt := mocktracer.Start()
	defer mt.Stop()

	span := ddtracer.StartSpan("op")
	event := NewEvent(ExceptionEvent, WithMessage("message"))
	AddToSpan(span, event)
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "op", spans[0].OperationName())

	eventsMap := spans[0].Tag("_dd.stack").(internal.MetaStructValue).Value.(map[string]any)
	require.Len(t, eventsMap, 1)

	eventsCat := eventsMap[string(ExceptionEvent)].([]*Event)
	require.Len(t, eventsCat, 1)

	require.Equal(t, *event, *eventsCat[0])
}

func TestMsgPackSerialization(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span := ddtracer.StartSpan("op")
	event := NewEvent(ExceptionEvent, WithMessage("message"), WithType("type"), WithID("id"))
	AddToSpan(span, event)
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	eventsMap := spans[0].Tag("_dd.stack").(internal.MetaStructValue).Value

	_, err := msgp.AppendIntf(nil, eventsMap)
	require.NoError(t, err)
}
