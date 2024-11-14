// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package actions

import (
	"net/http"

	"github.com/mitchellh/mapstructure"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// redirectActionParams are the dynamic parameters to be provided to a "redirect_request"
// action type upon invocation
type redirectActionParams struct {
	Location   string `mapstructure:"location,omitempty"`
	StatusCode int    `mapstructure:"status_code"`
}

func init() {
	registerActionHandler("redirect_request", NewRedirectAction)
}

func redirectParamsFromMap(params map[string]any) (redirectActionParams, error) {
	var parsedParams redirectActionParams
	err := mapstructure.WeakDecode(params, &parsedParams)
	return parsedParams, err
}

func newRedirectRequestAction(status int, loc string) *BlockHTTP {
	// Default to 303 if status is out of redirection codes bounds
	if status < http.StatusMultipleChoices || status >= http.StatusBadRequest {
		status = http.StatusSeeOther
	}

	// If location is not set we fall back on a default block action
	if loc == "" {
		return newHTTPBlockRequestAction(http.StatusForbidden, BlockingTemplateAuto)
	}

	redirectHandler := http.RedirectHandler(loc, status)
	return &BlockHTTP{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if UnwrapGetStatusCode(writer) != 0 {
			// The status code has already been set, so we can't change it, do nothing
			return
		}

		blocker, found := UnwrapBlocker(writer)
		if found {
			// We found our custom response writer, so we can block futur calls to Write and WriteHeader
			defer blocker()
		}

		redirectHandler.ServeHTTP(writer, request)
	})}
}

// NewRedirectAction creates an action for the "redirect_request" action type
func NewRedirectAction(params map[string]any) []Action {
	p, err := redirectParamsFromMap(params)
	if err != nil {
		log.Debug("appsec: couldn't decode redirect action parameters")
		return nil
	}
	return []Action{newRedirectRequestAction(p.StatusCode, p.Location)}
}
