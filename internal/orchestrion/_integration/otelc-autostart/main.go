// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command otelc-autostart proves that otelc starts the tracer on its own, the
// way it must for a real application.
//
// The integration harness cannot show this: it calls tracertest.Bootstrap itself,
// so a tracer is always running there regardless of what the build injected. This
// command never calls tracer.Start, so the span below only reaches an agent if
// the build injected the start for it.
//
//	# nothing is emitted: no tracer was ever started
//	go run ./otelc-autostart
//
//	# the span is emitted: otelc injected tracer.Start into main
//	otelc go run ./otelc-autostart
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

// spanName is the span the companion test looks for in the agent payload.
const spanName = "otelc.autostart"

func main() {
	// Lets the test tell a failed injection apart from a failed flush: no span
	// with enabled=false means no rule applied, no span with enabled=true points
	// at the tracer-start rule alone.
	fmt.Printf("otelc.Enabled=%t\n", otelc.Enabled())

	span := tracer.StartSpan(spanName)
	span.Finish()

	// The injected `defer tracer.Stop()` is what flushes the span, so main has to
	// return normally. os.Exit here would skip it and drop the span.
	fmt.Fprintln(os.Stderr, "otelc-autostart: done")
}
