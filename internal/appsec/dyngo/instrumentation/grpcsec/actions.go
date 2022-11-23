// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package grpcsec

import (
	"google.golang.org/grpc/codes"
)

type ActionParam interface{}

type action struct {
	id     string
	params ActionParam
}

// ActionHandler handles WAF actions registration and execution
type ActionHandler interface {
	RegisterAction(id string, params ActionParam)
	Exec(id string, op *HandlerOperation)
}

type actionsHandler struct {
	actions map[string]action
}

// NewActionsHandler returns an action handler holding the default ASM actions.
// Currently, only the default "block" action is supported
func NewActionsHandler() ActionHandler {
	defaultBlockAction := action{
		id: "block",
		params: BlockRequestParams{
			Status: codes.Aborted,
		},
	}
	// Register the default "block" action as specified in the RFC for HTTP blocking
	actions := map[string]action{defaultBlockAction.id: defaultBlockAction}

	return &actionsHandler{
		actions: actions,
	}
}

// RegisterAction registers a specific action to the actions handler. If the action kind is unknown
// the action will have no effect
func (h *actionsHandler) RegisterAction(id string, params ActionParam) {
	h.actions[id] = action{
		id:     id,
		params: params,
	}
}

// Exec executes the action identified by `id`
func (h *actionsHandler) Exec(id string, op *HandlerOperation) {
	a, ok := h.actions[id]
	if !ok {
		return
	}
	// Currently, only the "block_request" type is supported, so we only need to check for blockRequestParams
	if p, ok := a.params.(BlockRequestParams); ok {
		op.BlockedCode = &p.Status
	}
}

// BlockRequestParams is the parameter struct used to perform actions of kind ActionBlockRequest
type BlockRequestParams struct {
	ActionParam
	// Status is the return code to use when blocking the request
	Status codes.Code
}
