// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package router creates an httprouter router inside a dependency module.
package router

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// New returns a router serving GET /dep/:name.
func New() http.Handler {
	r := httprouter.New()
	r.GET("/dep/:name", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}
