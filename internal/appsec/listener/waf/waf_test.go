// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package waf

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	emitterwaf "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
)

func TestSetupActionHandlersStackTrace(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			op, _ := emitterwaf.StartContextOperation(context.Background(), trace.TestTagSetter{})
			feature := Feature{stackTrace: config.StackTraceConfig{
				Disabled: !enabled,
				MaxDepth: 1,
			}}
			feature.SetupActionHandlers(op)

			(&actions.StackTraceAction{}).EmitData(op)
			require.Empty(t, op.StackTraces())
			(&actions.StackTraceAction{Event: &stacktrace.Event{
				Category: stacktrace.ExploitEvent,
				ID:       "stack-id",
			}}).EmitData(op)

			stacks := op.StackTraces()
			if !enabled {
				require.Empty(t, stacks)
				return
			}
			require.Len(t, stacks, 1)
			require.Equal(t, stacktrace.ExploitEvent, stacks[0].Category)
			require.Equal(t, "stack-id", stacks[0].ID)
			require.Len(t, stacks[0].Frames, 1)
		})
	}
}
