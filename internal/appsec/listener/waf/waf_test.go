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
	op, _ := emitterwaf.StartContextOperation(context.Background(), trace.TestTagSetter{})
	feature := Feature{stackTrace: config.StackTraceConfig{MaxDepth: 1}}
	feature.SetupActionHandlers(op)

	(&actions.StackTraceAction{}).EmitData(op)
	require.Empty(t, op.StackTraces())

	event := stacktrace.NewEvent(
		stacktrace.ExploitEvent,
		stacktrace.WithID("stack-id"),
		stacktrace.WithDepth(1),
	)
	(&actions.StackTraceAction{Event: event}).EmitData(op)

	require.Equal(t, []*stacktrace.Event{event}, op.StackTraces())

	disabledOp, _ := emitterwaf.StartContextOperation(context.Background(), trace.TestTagSetter{})
	disabledFeature := Feature{stackTrace: config.StackTraceConfig{Disabled: true}}
	disabledFeature.SetupActionHandlers(disabledOp)
	(&actions.StackTraceAction{Event: event}).EmitData(disabledOp)
	require.Empty(t, disabledOp.StackTraces())
}
