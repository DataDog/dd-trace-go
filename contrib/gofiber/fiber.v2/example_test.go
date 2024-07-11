// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber_test

import (
	fibertrace "github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/gofiber/fiber/v2"
)

func Example() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Create a fiber v2 Router
	router := fiber.New()

	// Use the tracer middleware with the default service name "fiber".
	router.Use(fibertrace.Middleware())

	// Set up some endpoints.
	router.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("test")
	})

	// And start gathering request traces
	router.Listen(":8080")
}

func Example_withServiceName() {
	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	// Create a fiber v2 Router
	router := fiber.New()

	// Use the tracer middleware with your desired service name.
	router.Use(fibertrace.Middleware(fibertrace.WithService("fiber")))

	// Set up some endpoints.
	router.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("test")
	})

	// And start gathering request traces
	router.Listen(":8080")
}
