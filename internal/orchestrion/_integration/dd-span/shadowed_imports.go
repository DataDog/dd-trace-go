// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// This file imports the tracer and context itself. The functions below hide
// those imports with their own names, which the generated code must not
// resolve to.

var (
	_ context.Context
	_ *tracer.Span
)

//dd:span
func spanWithShadowedImportParam(context string) string {
	return context
}

//dd:span
func spanWithShadowedImportLocal() string {
	tracer := "local"
	return tracer
}
