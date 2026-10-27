// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp serves requests through gorilla/mux routers created in
// package main. TestRoutersInMain builds and runs it.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gorilla/mux"
)

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/main/router/{id}", ok).Methods(http.MethodGet)
	sub := router.PathPrefix("/main/sub").Subrouter()
	sub.HandleFunc("/ping", ok)
	serve(router, "/main/router/1")
	serve(sub, "/main/sub/ping")

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
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
