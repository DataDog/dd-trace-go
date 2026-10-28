// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for gofiber/fiber.v2. See otelc.yaml.
package otelc

import (
	_ "github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2"       // used by the otelc.yaml rules
	_ "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/otelc/v2" // used by the otelc.yaml rules
)
