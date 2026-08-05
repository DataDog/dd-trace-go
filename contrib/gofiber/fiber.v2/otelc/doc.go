// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for gofiber/fiber.v2, the otelc port of
// ../orchestrion.yml. A separate module because otelc/all blank-imports it
// directly, and it must be resolvable without pulling anything into the
// go.mod of applications that only import the contrib.
package otelc
