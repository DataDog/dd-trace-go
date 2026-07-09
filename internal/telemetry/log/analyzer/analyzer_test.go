// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package analyzer_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/log/analyzer"
)

// testFuncs mirrors DefaultFuncs but uses the fake package paths defined in
// testdata/src so that analysistest can resolve them without hitting the
// internal-package import restriction.
var testFuncs = []analyzer.FuncSpec{
	// internal/log equivalents
	{PkgPath: "example.com/fakelog", FuncName: "Error", MsgArgIndex: 0},
	{PkgPath: "example.com/fakelog", FuncName: "Warn", MsgArgIndex: 0},

	// internal/telemetry/log equivalents — pkg-level and Logger methods share the same name+pkg key.
	{PkgPath: "example.com/faketelemetrylog", FuncName: "Debug", MsgArgIndex: 0},
	{PkgPath: "example.com/faketelemetrylog", FuncName: "Warn", MsgArgIndex: 0},
	{PkgPath: "example.com/faketelemetrylog", FuncName: "Error", MsgArgIndex: 0},

	// helpers
	{PkgPath: "example.com/faketelemetrylog", FuncName: "ReportError", MsgArgIndex: 0},
	{PkgPath: "example.com/faketelemetrylog", FuncName: "ReportPanic", MsgArgIndex: 1},
}

// TestAnalyzer verifies that the analyzer flags non-constant message arguments
// in all protected functions and accepts constants and named-constant variables.
func TestAnalyzer(t *testing.T) {
	a := analyzer.New(testFuncs)
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, a, "logerrors")
}

// TestAnalyzerSkip verifies that packages listed in skipPkgs are silently
// ignored, even when they contain calls that would otherwise be flagged.
// This mirrors the real-world need to skip internal/telemetry/log itself,
// which delegates through the same function names with a variable message.
func TestAnalyzerSkip(t *testing.T) {
	// skippedpkg has non-constant message calls but no "// want" annotations.
	// If the skip mechanism works, no diagnostics are emitted and the test passes.
	// If the skip is broken, analysistest would fail on unexpected diagnostics.
	a := analyzer.New(testFuncs, "skippedpkg")
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, a, "skippedpkg")
}
