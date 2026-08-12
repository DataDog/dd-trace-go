// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package actions

import (
	"fmt"
	"log/slog"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

type (
	// Action is a generic interface that represents any WAF action
	Action interface {
		EmitData(op dyngo.Operation)
	}

	// Config contains configuration needed while constructing WAF actions. Its
	// zero value enables stack-trace actions with the default capture depth.
	Config struct {
		// StackTraceDisabled prevents stack-trace action creation.
		StackTraceDisabled bool
		// StackTraceDepth is the maximum number of frames captured by a stack-trace
		// action. A non-positive value uses the default depth.
		StackTraceDepth int
	}
)

type actionHandler func(map[string]any, Config) []Action

// actionHandlers is a map of action types to their respective handler functions
// It is populated by the init functions of the actions packages
var actionHandlers = map[string]actionHandler{}

func registerActionHandler(aType string, handler actionHandler) {
	if _, ok := actionHandlers[aType]; ok {
		log.Warn("appsec: action type `%s` already registered", aType)
		return
	}
	actionHandlers[aType] = handler
}

func withoutConfig(handler func(map[string]any) []Action) actionHandler {
	return func(params map[string]any, _ Config) []Action {
		return handler(params)
	}
}

// SendActionEvents sends the relevant actions to the operation's data listener.
// The first optional config controls action construction.
// It returns true if at least one of those actions require interrupting the request handler
// When SDKError is not nil, this error is sent to the op with EmitData so that the invoked SDK can return it
// returns whenever the request should be interrupted
func SendActionEvents(op dyngo.Operation, actions map[string]any, configs ...Config) bool {
	var cfg Config
	if len(configs) > 0 {
		cfg = configs[0]
	}

	var blocked bool
	for aType, params := range actions {
		log.Debug("appsec: processing %q action with params %v", aType, params) //nolint:gocritic
		params, ok := params.(map[string]any)
		if !ok {
			telemetrylog.Error("appsec: could not cast action params to map[string]any", slog.String("actual_type", fmt.Sprintf("%T", params)))
			continue
		}

		blocked = blocked || aType == "block_request"

		actionHandler, ok := actionHandlers[aType]
		if !ok {
			telemetrylog.Error("appsec: unknown action type", slog.String("action_type", aType))
			continue
		}

		for _, a := range actionHandler(params, cfg) {
			a.EmitData(op)
		}
	}

	return blocked
}
