// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdata

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const pkgComponentConst = instrumentation.PackageNetHTTP

var pkgComponentVar instrumentation.Package = instrumentation.PackageNetHTTP

// badCalls contains calls that pass an instrumentation.Package value as ext.Component.
// TestNoTypedComponentTag pins this file to the exact count of these calls.
func badCalls(span *tracer.Span, name string) {
	// Direct instrumentation.PackageXxx selector.
	_ = tracer.Tag(ext.Component, instrumentation.PackageNetHTTP)
	span.SetTag(ext.Component, instrumentation.PackageNetHTTP)

	// Conversion to instrumentation.Package.
	_ = tracer.Tag(ext.Component, instrumentation.Package(name))
	span.SetTag(ext.Component, instrumentation.Package("custom"))

	// Package-level typed const and var.
	_ = tracer.Tag(ext.Component, pkgComponentConst)
	span.SetTag(ext.Component, pkgComponentVar)

	// Local typed var and short declaration.
	var localTyped instrumentation.Package = "net/http"
	_ = tracer.Tag(ext.Component, localTyped)
	component := instrumentation.PackageNetHTTP
	span.SetTag(ext.Component, component)
}

// safeCalls contains calls that pass a plain string as ext.Component.
// None of these calls should match.
func safeCalls(span *tracer.Span) {
	// Explicit conversion to string.
	_ = tracer.Tag(ext.Component, string(instrumentation.PackageNetHTTP))
	span.SetTag(ext.Component, string(pkgComponentConst))

	// Plain string literals.
	_ = tracer.Tag(ext.Component, "net/http")
	span.SetTag(ext.Component, "net/http")
}

// safeScopeIsolation verifies that a typed local variable in another function
// does not leak into this function.
func safeScopeIsolation(span *tracer.Span) {
	component := "net/http"
	span.SetTag(ext.Component, component)
}

// safeShadowing verifies that local plain-string declarations shadow
// package-level typed declarations.
func safeShadowing(span *tracer.Span) {
	pkgComponentConst := "net/http"
	var pkgComponentVar = "net/http"
	_ = tracer.Tag(ext.Component, pkgComponentConst)
	span.SetTag(ext.Component, pkgComponentVar)
}
