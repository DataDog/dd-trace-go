// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package v5app creates an echo v5 instance inside a dependency module.
package v5app

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// New returns a handler serving GET /dep.
func New() http.Handler {
	e := echo.New()
	e.GET("/dep", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
	return e
}
