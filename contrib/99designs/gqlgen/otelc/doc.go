// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc (OpenTelemetry Go compile-time instrumentation)
// rules for the 99designs/gqlgen integration, the otelc port of
// ../orchestrion.yml. The rules wrap the constructor at its call site and need
// no hook functions of their own; the package still lives in its own module so
// otelc/all can name it directly, the same as every other contrib's otelc
// package.
package otelc
