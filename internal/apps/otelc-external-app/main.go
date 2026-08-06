// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command otelc-external-app stands in for a customer application built with
// otelc. Built by scripts/build_otelc_external_app.sh, which explains what it
// catches. Not a scenario, so it is not registered in scenario_test.go.
package main

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	// Referencing the tracer keeps the dependency real rather than a bare
	// require, so the build exercises the same package graph a customer
	// application pulls in.
	span := tracer.StartSpan("otelc.external.check")
	span.Finish()

	fmt.Println("otelc external application check")
}
