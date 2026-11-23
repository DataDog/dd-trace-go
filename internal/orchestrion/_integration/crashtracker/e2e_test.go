// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"testing"

	ct "github.com/DataDog/dd-trace-go/v2/crashtracker"
)

// TestMain intercepts re-executions of this binary that serve as crash-victim
// subprocesses (see TestCase.Run in crashtracker.go).
//
// The orchestrion.yml join-point uses test-main:false, which deliberately
// excludes test binaries' main() functions from injection. This subprocess role
// calls crashtracker.Start() explicitly to validate the crash pipeline
// (spawn → SetCrashOutput → panic → monitor → upload) under an orchestrion-built
// test binary. TestCrashtrackerMainInjection validates injection into a real
// non-test main function.
func TestMain(m *testing.M) {
	switch role := os.Getenv(e2eRoleEnv); role {
	case crashRoleOrch:
		// Explicit Start() — orchestrion does not inject into test binaries.
		if err := ct.Start(); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		panic(orchCrashMsg)
	case "":
		// Not a re-exec: run the package's own tests normally.
	default:
		// A role was set but didn't match any case above -- a missing case for
		// a newly added role, most likely. Falling through to m.Run() here
		// would run the full test suite inside what the parent expects to be a
		// crash-victim subprocess, and the parent would wait out its full
		// timeout for a report that a test suite, not a victim, can't produce.
		os.Stderr.WriteString("unknown e2e role: " + role + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
