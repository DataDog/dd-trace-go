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
