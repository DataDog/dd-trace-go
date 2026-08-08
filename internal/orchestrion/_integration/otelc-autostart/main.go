// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command otelc-autostart proves that otelc starts the tracer on its own, the
// way it must for a real application.
//
// The integration harness cannot show this: it calls tracertest.Bootstrap
// itself, so a tracer is always running there regardless of whether the build
// injected one. This command deliberately never calls tracer.Start, so the only
// way the span below reaches an agent is if the build injected the start for it.
//
//	# nothing is emitted: no tracer was ever started
//	go run ./otelc-autostart
//
//	# the span is emitted: otelc injected tracer.Start into main
//	otelc go run ./otelc-autostart
//
// The companion test drives both of those and asserts on what a stub agent
// received, so the negative build is what distinguishes "otelc injected the
// tracer" from "the app traced itself".
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
	// Reported on stdout so the test can tell a failed injection apart from a
	// failed flush: no span AND enabled=false means the rules never applied,
	// while no span with enabled=true points at the tracer-start rule.
	fmt.Printf("otelc.Enabled=%t\n", otelc.Enabled())

	span := tracer.StartSpan(spanName)
	span.Finish()

	// The tracer is stopped by the injected `defer tracer.Stop()`, which is what
	// flushes the span. Returning from main normally is therefore required;
	// os.Exit here would skip the deferred stop and drop the span.
	fmt.Fprintln(os.Stderr, "otelc-autostart: done")
}
