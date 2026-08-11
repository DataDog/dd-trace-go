// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the build-mode proof for the otelc lane. It is not a
// harness.TestCase: it asserts nothing about spans, only that the toolchain
// wove anything at all.
//
// It exists to make a broken otelc build fail loudly rather than quietly. Every
// other suite in this directory skips or degrades when instrumentation is
// missing, so a lane where otelc silently stopped applying its rules would
// otherwise report a wall of passes.
package otelc

import (
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

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
