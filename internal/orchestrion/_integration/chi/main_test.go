// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/stubagent"
)

// TestRoutersInMain covers the otelc rules with target: main for chi and
// chi.v5. The harness suites only reach the $root rules, since the package
// under test is never main.
func TestRoutersInMain(t *testing.T) {
	if _, err := exec.LookPath("otelc"); err != nil {
		t.Skip("otelc is not on PATH; install it from " +
			"github.com/open-telemetry/opentelemetry-go-compile-instrumentation")
	}

	// Built from the module directory so otelc finds ../otel.instrumentation.go.
	moduleDir, err := filepath.Abs("..")
	require.NoError(t, err)

	bin := filepath.Join(t.TempDir(), "mainapp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// A private work dir keeps this build from fighting over .otelc-build with
	// whatever invoked the test.
	build := exec.Command("otelc", "--work-dir", t.TempDir(),
		"go", "build", "-o", bin, "./chi/mainapp")
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

	for _, resource := range []string{
		"GET /v4/router",
		"GET /v4/mux",
		"GET /v5/router",
		"GET /v5/mux",
	} {
		assert.Truef(t, agent.Reported(resource),
			"no %q span reached the agent across %d payload(s)", resource, agent.RequestCount())
	}
}
