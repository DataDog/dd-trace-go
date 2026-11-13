// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package fiberdep creates a Fiber app inside a dependency module.
package fiberdep

import "github.com/gofiber/fiber/v2"

// New returns an app serving GET /dep.
func New() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/dep", func(c *fiber.Ctx) error { return c.SendString("dep") })
	return app
}
