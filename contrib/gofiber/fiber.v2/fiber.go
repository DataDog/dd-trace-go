// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package fiber provides tracing functions for tracing the fiber package (https://github.com/gofiber/fiber).
package fiber // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gofiber/fiber.v2"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2"

	"github.com/gofiber/fiber/v2"
)

// Middleware returns middleware that will trace incoming requests.
func Middleware(opts ...Option) func(c *fiber.Ctx) error {
	return v2.Middleware(opts...)
}
