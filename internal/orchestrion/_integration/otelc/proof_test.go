// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestBuiltWithOtelc asserts that otelc wove something into this binary. It
// checks nothing about spans, unlike the harness.TestCase suites.
//
// Every other suite in this directory skips or degrades when instrumentation is
// missing, so a lane where otelc stopped applying its rules would otherwise
// report a wall of passes.
func TestBuiltWithOtelc(t *testing.T) {
	if built.WithOrchestrion {
		t.Skip("this package proves the otelc lane; this binary was built with orchestrion")
	}
	if !otelc.Enabled() {
		t.Fatal("not built with otelc: run this package under `otelc go test`. " +
			"A plain `go test` cannot prove anything here, and a failure means " +
			"the assign_value rule in ddtrace/tracer/otelc.yaml did not apply")
	}
}
