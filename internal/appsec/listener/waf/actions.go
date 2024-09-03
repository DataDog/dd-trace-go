// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// SendActionEvents sends the relevant actions to the operation's data listener.
// It returns true if at least one of those actions require interrupting the request handler
// When SDKError is not nil, this error is sent to the op with EmitData so that the invoked SDK can return it
func SendActionEvents(op dyngo.Operation, actions map[string]any) (interrupt bool) {
	for aType, params := range actions {
		log.Debug("appsec: processing %s action with params %v", aType, params)
		actionArray := ActionsFromEntry(aType, params)
		if actionArray == nil {
			log.Debug("cannot process %s action with params %v", aType, params)
			continue
		}
		for _, a := range actionArray {
			a.EmitData(op)
			interrupt = interrupt || a.Blocking()
		}
	}

	// If any of the actions are supposed to interrupt the request, emit a blocking event for the SDK operations
	// to return an error.
	if interrupt {
		dyngo.EmitData(op, &events.BlockingSecurityEvent{})
	}

	return interrupt
}

// ActionsFromEntry returns one or several actions generated from the Feature returned action entry
// Several actions are returned when the action is of block_request type since we could be blocking HTTP or GRPC
func ActionsFromEntry(actionType string, params any) []sharedsec.Action {
	p, ok := params.(map[string]any)
	if !ok {
		return nil
	}
	switch actionType {
	case "block_request":
		return sharedsec.NewBlockAction(p)
	case "redirect_request":
		return []sharedsec.Action{sharedsec.NewRedirectAction(p)}
	case "generate_stack":
		return []sharedsec.Action{sharedsec.NewStackTraceAction(p)}

	default:
		log.Debug("appsec: unknown action type `%s`", actionType)
		return nil
	}
}
