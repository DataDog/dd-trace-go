// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the gin engine constructors from package main, where
// only the otelc rules with target: main apply. TestEnginesInMain builds and
// runs it.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode) // Silence start-up logging
	serve(route(gin.New(), "/main/new"), "/main/new")
	serve(route(gin.Default(), "/main/default"), "/main/default")
	serveAliased()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func route(e *gin.Engine, path string) http.Handler {
	e.GET(path, func(c *gin.Context) { c.Status(http.StatusOK) })
	return e
}

func serve(h http.Handler, path string) {
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
}
