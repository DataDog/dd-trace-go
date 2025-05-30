// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2"

	"github.com/labstack/echo/v4"
)

// envServerErrorStatuses is the name of the env var used to specify error status codes on http server spans
const envServerErrorStatuses = "DD_TRACE_HTTP_SERVER_ERROR_STATUSES"

// Middleware returns echo middleware which will trace incoming requests.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	return v2.Middleware(opts...)
}
