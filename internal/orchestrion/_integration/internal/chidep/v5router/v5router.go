// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package v5router creates a chi router inside a dependency module.
package v5router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// New returns a router serving GET /dep.
func New() http.Handler {
	r := chi.NewRouter()
	r.Get("/dep", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return r
}
