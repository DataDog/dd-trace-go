// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package echo

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/labstack/echo"
)

// To start tracing requests, add the trace middleware to your echo router.
func Example() {
	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(Middleware(WithServiceName("my-web-app")))

	// Set up an endpoint.
	r.GET("/hello", func(c echo.Context) error {
		return c.String(200, "hello world!")
	})

	// ...and listen for incoming requests
	r.Start(":8080")
}

// An example illustrating tracing a child operation within the main context.
func Example_spanFromContext() {
	// Create a new instance of echo
	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(Middleware(WithServiceName("image-encoder")))

	// Set up some endpoints.
	r.GET("/image/encode", func(c echo.Context) error {
		// create a child span to track an operation
		span, _ := tracer.StartSpanFromContext(c.Request().Context(), "image.encode")

		// encode an image ...

		// finish the child span
		span.Finish()

		return c.String(200, "ok!")
	})
}
