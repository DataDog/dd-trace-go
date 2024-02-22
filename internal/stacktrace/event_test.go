// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"testing"
)

func TestNewEvent(t *testing.T) {
	event := NewEvent(ExceptionEvent, "", "message")
	require.Equal(t, ExceptionEvent, event.Category)
	require.Equal(t, "go", event.Language)
	require.Equal(t, "message", event.Message)
	require.GreaterOrEqual(t, len(event.Frames), 3)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestNewEvent", event.Frames[len(event.Frames)-1].Function)
}

func TestEventToSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span := ddtracer.StartSpan("op")
	event := NewEvent(ExceptionEvent, "", "message")
	event.AddToSpan(span)
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "op", spans[0].OperationName())
	require.Equal(t, *event, spans[0].Tag("_dd.stack.exception"))
}
