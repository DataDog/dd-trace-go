// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the chi router constructors from package main, where
// only the otelc rules with target: main apply. go test never builds a package
// main, so TestRoutersInMain builds and runs this one.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/go-chi/chi"
)

func main() {
	serve(route(chi.NewRouter(), "/v4/router"), "/v4/router")
	serve(route(chi.NewMux(), "/v4/mux"), "/v4/mux")
	serveV5()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func route(mux *chi.Mux, path string) http.Handler {
	mux.Get(path, ok)
	return mux
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func serve(h http.Handler, path string) {
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
}
