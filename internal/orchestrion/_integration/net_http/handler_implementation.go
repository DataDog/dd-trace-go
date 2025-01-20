// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"net/http"
	"testing"
)

type customHandler struct {
	handleRoot func(w http.ResponseWriter, r *http.Request)
	handleHit  func(w http.ResponseWriter, r *http.Request)
}

func (c *customHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		c.handleRoot(w, r)
		return

	case "/hit":
		c.handleHit(w, r)
		return

	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
}

type TestCaseHandlerImplementation struct {
	base
}

func (tc *TestCaseHandlerImplementation) Setup(ctx context.Context, t *testing.T) {
	tc.handler = &customHandler{
		handleRoot: tc.handleRoot,
		handleHit:  tc.handleHit,
	}
	tc.base.Setup(ctx, t)
}
