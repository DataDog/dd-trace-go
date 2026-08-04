// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build tools

// Blank-importing this package from an application's otel.instrumentation.go
// enables all of dd-trace-go's otelc instrumentation, the way
// github.com/DataDog/dd-trace-go/orchestrion/all/v2 does for orchestrion.
package all

import (
	// Reaches dd-trace-go's own tool file through this package's module root,
	// which is what pulls in the tracer, GLS and AppSec rules.
	_ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)
