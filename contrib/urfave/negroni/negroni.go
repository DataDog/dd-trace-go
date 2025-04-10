// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package negroni provides helper functions for tracing the urfave/negroni package (https://github.com/urfave/negroni).
package negroni

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/urfave/negroni/v2"
)

// DatadogMiddleware returns middleware that will trace incoming requests.
type DatadogMiddleware = v2.DatadogMiddleware

// Middleware create the negroni middleware that will trace incoming requests
func Middleware(opts ...Option) *DatadogMiddleware {
	return v2.Middleware(opts...)
}
