// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp creates Fiber apps in package main, where the go test
// suites never run the rules. TestAppsInMain builds and runs it.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gofiber/fiber/v2"
)

// The rule also applies to a package-level initializer.
var pkgApp = fiber.New(fiber.Config{DisableStartupMessage: true})

func main() {
	serve(fiber.New(fiber.Config{DisableStartupMessage: true}), "/main")
	serve(pkgApp, "/var")
	serveAliased()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func serve(app *fiber.App, path string) {
	app.Get(path, func(c *fiber.Ctx) error { return c.SendString("ok") })
	res, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		panic(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("GET %s: status %d", path, res.StatusCode))
	}
}
