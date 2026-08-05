// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build tools

// Blank-importing this package from an application's otel.instrumentation.go
// enables all of dd-trace-go's otelc instrumentation, the way
// github.com/DataDog/dd-trace-go/orchestrion/all/v2 does for orchestrion.
//
// Each import contributes the rule files sitting next to that package.
package all

import (
	// Build-mode flag and the GLS storage woven into the runtime package.
	_ "github.com/DataDog/dd-trace-go/v2/internal/otelc"

	// Tracer lifecycle and the span GLS lifecycle.
	_ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	// AppSec operation GLS lifecycle.
	_ "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"

	// gorilla/mux Router.ServeHTTP tracing.
	_ "github.com/DataDog/dd-trace-go/contrib/gorilla/mux/otelc/v2"
)
