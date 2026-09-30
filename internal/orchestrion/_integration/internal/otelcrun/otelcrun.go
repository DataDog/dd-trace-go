// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelcrun builds a package main of this module with otelc and runs it
// against a stub agent. Rules with target: main only apply there, and go test
// never builds a package main.
package otelcrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/stubagent"
)

// Run builds pkg, a path relative to the module root such as "./chi/mainapp",
// runs it to completion, and returns the agent it reported to. It skips the
// test when otelc is not on PATH.
func Run(t *testing.T, pkg string) *stubagent.Agent {
	t.Helper()
	if _, err := exec.LookPath("otelc"); err != nil {
		t.Skip("otelc is not on PATH; install it from " +
			"github.com/open-telemetry/opentelemetry-go-compile-instrumentation")
	}

	// The test runs in its package directory, inside this module.
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	require.NoError(t, err)
	moduleDir := filepath.Dir(strings.TrimSpace(string(gomod)))

	bin := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// A private work dir keeps this build from fighting over .otelc-build with
	// whatever invoked the test.
	build := exec.Command("otelc", "--work-dir", t.TempDir(), "go", "build", "-o", bin, pkg)
	build.Dir = moduleDir
	build.Env = append(os.Environ(), "GOWORK=off")
	t.Log("building (slow: otelc recompiles the dependency closure)")
	out, err := build.CombinedOutput()
	require.NoErrorf(t, err, "build failed:\n%s", out)

	agent := stubagent.New(t)
	run := exec.Command(bin)
	run.Env = append(os.Environ(),
		"DD_TRACE_AGENT_URL="+agent.URL(),
		"DD_TRACE_STARTUP_LOGS=false",
	)
	runOut, err := run.CombinedOutput()
	require.NoErrorf(t, err, "running the app failed:\n%s", runOut)
	return agent
}
