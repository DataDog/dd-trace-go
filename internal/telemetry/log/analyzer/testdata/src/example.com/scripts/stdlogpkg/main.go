// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package stdlogpkg simulates a generator/script file, one of the paths where
// golangci-lint's depguard config allows the standard library "log" package
// (its import path contains "/scripts/", matching stdLogAllowedFile).
package stdlogpkg

import "log"

type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

func good(err *customError) {
	log.Fatalf("failed: %s", err.Error())
}

func bad(name string) {
	log.Printf("value: %v", name) // want "exposes uncontrolled data"
}

func badSpread(args ...any) {
	// Spread calls have no single "last argument" to check; must not crash.
	log.Printf("value: %v", args...)
}
