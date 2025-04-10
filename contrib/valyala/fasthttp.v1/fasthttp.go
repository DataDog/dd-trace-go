// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package fasthttp provides functions to trace the valyala/fasthttp package (https://github.com/valyala/fasthttp)
package fasthttp // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/valyala/fasthttp.v1"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
	"github.com/valyala/fasthttp"
)

const componentName = "valyala/fasthttp"

// WrapHandler wraps a fasthttp.RequestHandler with tracing middleware
func WrapHandler(h fasthttp.RequestHandler, opts ...Option) fasthttp.RequestHandler {
	return v2.WrapHandler(h, opts...)
}
