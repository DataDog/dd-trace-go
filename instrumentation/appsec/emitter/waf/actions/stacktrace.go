// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package actions

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
)

func init() {
	registerActionHandler("generate_stack", newStackTraceAction)
}

// StackTraceAction contains a generated stack-trace event.
type StackTraceAction struct {
	Event *stacktrace.Event
}

func (a *StackTraceAction) EmitData(op dyngo.Operation) { dyngo.EmitData(op, a) }

// NewStackTraceAction immediately captures a stack trace for a generate_stack
// action using the default capture depth.
func NewStackTraceAction(params map[string]any) []Action {
	return newStackTraceAction(params, Config{})
}

func newStackTraceAction(params map[string]any, cfg Config) []Action {
	if cfg.StackTraceDisabled {
		return nil
	}
	id, ok := params["stack_id"]
	if !ok {
		log.Debug("appsec: could not read stack_id parameter for generate_stack action")
		return nil
	}

	strID, ok := id.(string)
	if !ok {
		log.Debug("appsec: could not cast stacktrace ID to string")
		return nil
	}

	return []Action{
		&StackTraceAction{Event: stacktrace.NewEvent(
			stacktrace.ExploitEvent,
			stacktrace.WithID(strID),
			stacktrace.WithDepth(cfg.StackTraceDepth),
		)},
	}
}
