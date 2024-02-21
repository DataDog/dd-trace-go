// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stacktrace

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNewEvent(t *testing.T) {
	event := NewException("message")
	require.Equal(t, ExceptionEvent, event.Category)
	require.Equal(t, "go", event.Language)
	require.Equal(t, "message", event.Message)
	require.GreaterOrEqual(t, len(event.Frames), 3)
	require.Equal(t, "gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace.TestNewEvent", event.Frames[len(event.Frames)-1].Function)
}
