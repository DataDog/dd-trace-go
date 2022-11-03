// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build appsec
// +build appsec

package httpsec

import (
	"net/http"
)

// Action is used to identify any action kind
type Action interface {
	isAction()
}

// BlockRequestAction is the parameter struct used to perform actions of kind ActionBlockRequest
type BlockRequestAction struct {
	Action
	// Status is the return code to use when blocking the request
	Status int
	// Template is the payload template to use to write the response (html or json)
	Template string
	// handler is the http handler be used to block the request (see wrap())
	handler http.Handler
}

func (*BlockRequestAction) isAction() {}

func blockedPayload(a *BlockRequestAction) []byte {
	payload := BlockedTemplateJSON
	if a.Template == "html" {
		payload = BlockedTemplateHTML
	}
	return payload
}

// ActionsHandler handles actions registration and their application to operations
type ActionsHandler struct {
	actions map[string]Action
}

// NewActionsHandler returns an action handler holding the default ASM actions.
// Currently, only the default "block" action is supported
func NewActionsHandler() *ActionsHandler {
	handler := ActionsHandler{
		actions: map[string]Action{},
	}
	// Register the default "block" action as specified in the RFC for HTTP blocking
	handler.RegisterAction("block", &BlockRequestAction{
		Status:   403,
		Template: "html",
	})

	return &handler
}

// RegisterAction registers a specific action to the handler. If the action kind is unknown
// the action will not be registered
func (h *ActionsHandler) RegisterAction(id string, action Action) {
	switch a := action.(type) {
	case *BlockRequestAction:
		payload := blockedPayload(a)
		a.handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Write(payload)
			writer.WriteHeader(a.Status)
		})
		h.actions[id] = a
	default:
		break
	}
}

// Apply applies the action identified by `id` for the given operation
func (h *ActionsHandler) Apply(id string, op *Operation) {
	a, ok := h.actions[id]
	if !ok {
		return
	}
	op.actions = append(op.actions, a)
}
