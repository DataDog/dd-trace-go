// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command otelc-autostart checks the otelc rules that only apply to a package
// main, which the integration harness never builds:
//
//   - the tracer start rule. The harness calls tracertest.Bootstrap itself, so a
//     tracer is always running there regardless of what the build injected.
//     This command never calls tracer.Start, so its spans only reach an agent
//     if the build injected the start.
//
//   - //dd:span in the file that holds main. That file imports the tracer, and
//     main declares a `tracer` local, so both rules must use their own import
//     of the tracer.
//
//   - //dd:span on main itself. The tracer has to start before that span and
//     stop after it finishes, or the span is lost.
//
//     # nothing is emitted: no tracer was ever started
//     go run ./otelc-autostart
//
//     # the spans are emitted: otelc injected tracer.Start into main
//     otelc go run ./otelc-autostart
//
// The companion test drives both and asserts on what a stub agent received. The
// negative build is what separates "otelc injected the tracer" from "the app
// traced itself".
package main

import (
	"fmt"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// The spans the companion test looks for in the agent payload.
const (
	spanName      = "otelc.autostart"
	directiveSpan = "otelc.autostart.directive"
	mainSpan      = "otelc.autostart.main"
)

//dd:span span.name:otelc.autostart.main
func main() {
	// Lets the test tell a failed injection apart from a failed flush: no span
	// with enabled=false means no rule applied, no span with enabled=true points
	// at the tracer-start rule alone.
	fmt.Printf("otelc.Enabled=%t\n", otelc.Enabled())

	// A local named like a package that a rule imports must not break the
	// build. Both rules inject code into this function.
	tracer := tracer.StartSpan(spanName)
	tracer.Finish()
	annotated()

	// The injected `defer tracer.Stop()` is what flushes the spans, so main has
	// to return normally. os.Exit here would skip it and drop them.
	fmt.Fprintln(os.Stderr, "otelc-autostart: done")
}

//dd:span span.name:otelc.autostart.directive
func annotated() {}
