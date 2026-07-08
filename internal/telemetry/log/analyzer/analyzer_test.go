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
// testdata/src so that analysistest can resolve them without hitting internal
// package restrictions.
var testFuncs = []analyzer.FuncSpec{
	{PkgPath: "example.com/fakelog", FuncName: "Error", MsgArgIndex: 0},
	{PkgPath: "example.com/fakelog", FuncName: "Warn", MsgArgIndex: 0},
	{PkgPath: "example.com/faketelemetrylog", FuncName: "ReportError", MsgArgIndex: 0},
	{PkgPath: "example.com/faketelemetrylog", FuncName: "ReportPanic", MsgArgIndex: 1},
}

func TestAnalyzer(t *testing.T) {
	a := analyzer.New(testFuncs)
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, a, "logerrors")
}
