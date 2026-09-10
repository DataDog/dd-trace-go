// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package actions

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
)

func TestNewStackTraceAction(t *testing.T) {
	actions := newStackTraceAction(map[string]any{"stack_id": "stack-id"}, Config{StackTraceDepth: 1})
	require.Len(t, actions, 1)
	action, ok := actions[0].(*StackTraceAction)
	require.True(t, ok)
	require.Equal(t, stacktrace.ExploitEvent, action.Event.Category)
	require.Equal(t, "stack-id", action.Event.ID)
	require.Len(t, action.Event.Frames, 1)

	require.Nil(t, newStackTraceAction(map[string]any{"stack_id": "stack-id"}, Config{StackTraceDisabled: true}))
	require.Nil(t, NewStackTraceAction(map[string]any{}))
	require.Nil(t, NewStackTraceAction(map[string]any{"stack_id": 1}))
}
