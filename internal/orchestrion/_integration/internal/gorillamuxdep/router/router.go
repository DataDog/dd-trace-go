// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package router creates a gorilla/mux router inside a dependency module.
package router

import (
	"net/http"

	"github.com/gorilla/mux"
)

// New returns a router serving GET /dep/{id}.
func New() http.Handler {
	r := mux.NewRouter()
	r.HandleFunc("/dep/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)
	return r
}
