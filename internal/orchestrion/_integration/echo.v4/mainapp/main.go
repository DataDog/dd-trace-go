// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls echo.New from package main, which the harness suites
// never build. TestEchoInMain builds and runs it.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
)

func main() {
	e := echo.New()
	e.GET("/v4/main", ok)
	serve(e, "/v4/main")
	serveAliased()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func ok(c echo.Context) error {
	return c.NoContent(http.StatusOK)
}

func serve(h http.Handler, path string) {
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
}
