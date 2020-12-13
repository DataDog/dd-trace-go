// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package fiber provides tracing functions for tracing the fiber package (https://github.com/gofiber/fiber).
package fiber // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/fiber/fiber"

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Middleware returns middleware that will trace incoming requests.
func Middleware(opts ...Option) func(c *fiber.Ctx) error {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return func(c *fiber.Ctx) error {
		opts := []ddtrace.StartSpanOption{
			tracer.SpanType(ext.SpanTypeWeb),
			tracer.ServiceName(cfg.serviceName),
			tracer.Tag(ext.HTTPMethod, c.Context().Method),
			tracer.Tag(ext.HTTPURL, c.Context().Path),
			tracer.Measured(),
		}
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}

		opts = append(opts, cfg.spanOpts...)
		span, _ := tracer.StartSpanFromContext(c.Context(), "http.request", opts...)
		defer span.Finish()

		err := c.Next()

		//set the resource name (URI) as we get it only once the handler is executed
		resourceName := c.Path()
		if resourceName == "" {
			resourceName = "unknown"
		}
		resourceName = c.Method() + " " + resourceName
		span.SetTag(ext.ResourceName, resourceName)

		// set the status code
		status := c.Response().StatusCode()
		// 0 status means one has not yet been sent in which case net/http library will write StatusOK
		if status == 0 {
			status = http.StatusOK
		}
		span.SetTag(ext.HTTPCode, strconv.Itoa(status))

		if cfg.isStatusError(status) {
			// mark 5xx server error
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
		}

		return err
	}
}
