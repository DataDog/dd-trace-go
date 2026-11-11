// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gorilla/mux"
	"github.com/julienschmidt/httprouter"
)

// The gorilla/mux and httprouter rules inject code into the library that calls
// the contrib, so the rules module has to link the contrib. Their suites import
// the contrib themselves and link it either way. This command doesn't import
// it, so it fails to link or reports no span when the rules module doesn't.
const (
	muxResource    = "GET /mux/{id}"
	routerResource = "GET /router/:id"
)

func serveRouters() {
	m := mux.NewRouter()
	m.HandleFunc("/mux/{id}", ok).Methods(http.MethodGet)
	serve(m, "/mux/1")

	r := httprouter.New()
	r.GET("/router/:id", func(w http.ResponseWriter, req *http.Request, _ httprouter.Params) { ok(w, req) })
	serve(r, "/router/1")
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func serve(h http.Handler, path string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		panic(fmt.Sprintf("GET %s: status %d", path, rec.Code))
	}
}
