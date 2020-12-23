// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package fiber_test

import (
	"github.com/gofiber/fiber/v2"

	fibertrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gofiber/fiber.v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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
	router.Use(fibertrace.Middleware(fibertrace.WithServiceName("fiber")))

	// Set up some endpoints.
	router.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("test")
	})

	// And start gathering request traces
	router.Listen(":8080")
}
