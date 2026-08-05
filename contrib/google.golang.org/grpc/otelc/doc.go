// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for
// github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2, the otelc
// port of that contrib's orchestrion.yml.
//
// It is its own module, following the same layout as every other otelc-ported
// contrib, so otelc/all can name it directly: otelc stops recursing into a
// package's directory tree at a nested module boundary. These rules append
// grpc interceptors at call sites and need no hook functions, but the module
// stays separate to match the convention.
package otelc
