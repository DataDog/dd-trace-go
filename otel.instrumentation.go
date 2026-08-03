// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build tools

// This is dd-trace-go's own otelc tool file, and it is what makes the foundation
// self-installing. otelc resolves each package named in an application's tool
// file, then looks for a tool file at that package's MODULE root and recurses
// into it. So any application whose tool file imports a single dd-trace-go
// package reaches this file, and through it every rule below.
//
// That indirection is the point: the rules for internal/otelc and
// instrumentation/appsec/dyngo target packages an application cannot import
// itself, so they could never be named in an application's own tool file.
//
// The package clause matches gosum.go so the repository root never looks like two
// conflicting packages; both files are excluded from every build anyway.
package main

// Each import contributes the rule files sitting next to that package.
import (
	// Build-mode flag (internal/otelc/otelc.yaml) and the GLS storage layer woven
	// into the runtime package (internal/otelc/gls.otelc.yaml).
	_ "github.com/DataDog/dd-trace-go/v2/internal/otelc"

	// Tracer lifecycle (ddtrace/tracer/otelc.yaml) and the span GLS lifecycle
	// (ddtrace/tracer/gls.otelc.yaml).
	_ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	// AppSec operation GLS lifecycle (instrumentation/appsec/dyngo/gls.otelc.yaml).
	_ "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
)
