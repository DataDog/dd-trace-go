// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build tools

// Enables otelc instrumentation for this module, the otelc counterpart of
// orchestrion.tool.go.
//
// The single import below is enough for the whole foundation: otelc finds the
// tool file at dd-trace-go's module root and recurses into it, picking up the
// tracer, GLS and AppSec rules. Per-integration imports get added here as
// contribs are migrated, the same way orchestrion.tool.go lists them.
package tools

import (
	_ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer" // foundation
)
