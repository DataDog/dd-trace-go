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
	actions := NewStackTraceAction(map[string]any{"stack_id": "stack-id"})
	require.Equal(t, []Action{&StackTraceAction{Event: &stacktrace.Event{
		Category: stacktrace.ExploitEvent,
		ID:       "stack-id",
	}}}, actions)

	require.Nil(t, NewStackTraceAction(map[string]any{}))
	require.Nil(t, NewStackTraceAction(map[string]any{"stack_id": 1}))
}
