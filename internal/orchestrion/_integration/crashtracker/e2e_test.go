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
// Note: the orchestrion.yml join-point uses test-main: false, which deliberately
// excludes test binaries' main() functions from injection. The subprocess role
// therefore calls crashtracker.Start() explicitly rather than relying on the
// orchestrion-injected call. The test validates the crash pipeline
// (spawn → SetCrashOutput → panic → monitor → upload) in the orchestrion
// integration test environment. Injection into non-test binaries is exercised
// by the orchestrion toolexec/driver build modes against real application code.
func TestMain(m *testing.M) {
	switch os.Getenv(e2eRoleEnv) {
	case crashRoleOrch:
		// Explicit Start() — orchestrion does not inject into test binaries.
		if err := ct.Start(); err != nil {
			os.Stderr.WriteString("crashtracker.Start: " + err.Error() + "\n")
			os.Exit(1)
		}
		panic(orchCrashMsg)
	}
	os.Exit(m.Run())
}
