// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"testing"
)

// TestMain intercepts re-executions of this orchestrion-built binary that serve
// as crash-victim subprocesses (see TestCase.Run in crashtracker.go).
//
// When _CRASHTRACKER_E2E_ORCH=panic the subprocess panics without calling
// crashtracker.Start() — relying entirely on the orchestrion-injected call that
// fires before TestMain runs. This proves the orchestrion.yml aspect works.
func TestMain(m *testing.M) {
	switch os.Getenv(e2eRoleEnv) {
	case crashRoleOrch:
		// Orchestrion already injected crashtracker.Start() before TestMain.
		// We do NOT call Start() here intentionally — that's the whole point.
		panic(orchCrashMsg)
	}
	os.Exit(m.Run())
}
