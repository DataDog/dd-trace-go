// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package engine creates a gin engine inside a dependency module.
package engine

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// New returns an engine serving GET /dep.
func New() http.Handler {
	e := gin.New()
	e.GET("/dep", func(c *gin.Context) { c.Status(http.StatusOK) })
	return e
}
