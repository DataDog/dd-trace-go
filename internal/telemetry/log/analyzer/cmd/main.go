// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// errlog-vet is a standalone go vet tool bundling the SDK logging safety
// analyzers: constant message arguments (constantlogmsg), telemetry PII
// scrubbing (telemetrysafety), and unsafe %v/%+v/%#v format verbs
// (logformatverbs). The latter two replace the ruleguard rules formerly in
// rules/telemetry_rules.go and rules/logging_rules.go.
//
// Usage:
//
//	go build -o errlog-vet github.com/DataDog/dd-trace-go/v2/internal/telemetry/log/analyzer/cmd
//	go vet -vettool=./errlog-vet ./...
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/log/analyzer"
)

func main() {
	multichecker.Main(
		analyzer.Analyzer,
		analyzer.TelemetrySafetyAnalyzer,
		analyzer.FormatVerbsAnalyzer,
	)
}
