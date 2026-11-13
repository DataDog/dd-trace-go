// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fiber

import (
	"context"
	"testing"

	gofiber "github.com/gofiber/fiber/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that an app is traced exactly once when fiber
// is imported under another name.
type TestCaseAliasedImport struct {
	app *gofiber.App
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	tc.app = gofiber.New(gofiber.Config{DisableStartupMessage: true})
	tc.app.Get("/alias", func(c *gofiber.Ctx) error { return c.SendString("ok") })
}

func (tc *TestCaseAliasedImport) Run(_ context.Context, t *testing.T) {
	get(t, tc.app, "/alias")
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 1
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return expectFiberSpan("GET /alias")
}
