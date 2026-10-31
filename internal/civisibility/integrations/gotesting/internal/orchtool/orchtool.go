// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package orchtool resolves the orchestrion CLI for the fixture tests that
// drive it in subprocesses.
package orchtool

import "os"

// OrchestrionBinEnv carries the path of the orchestrion binary that the CI
// install action builds for the fixture tests. It has no DD_ prefix on
// purpose: it is a test-harness variable, not a tracer configuration, so it
// must stay out of the supported-configurations list.
const OrchestrionBinEnv = "ORCHESTRION_FIXTURE_BIN"

// Locate returns the path of the orchestrion binary exported through
// OrchestrionBinEnv, or the empty string when the variable is unset. CI's
// install action (.github/actions/install-orchestrion) exports the binary it
// builds inside the fixture module, so the binary resolves the same
// dependencies that the `go run` fallback resolves.
//
// The binary is never resolved from PATH. An orchestrion binary installed
// outside the fixture module can report the pinned version and still fail to
// build the module: a standalone install resolves older x/tools than the
// fixture module does, and older x/tools cannot read the export data that a
// newer Go toolchain writes.
func Locate() string {
	// internal/env reads supported tracer configurations only, and this
	// CI-internal variable is not one.
	//nolint:forbidigo
	return os.Getenv(OrchestrionBinEnv)
}

// Command returns the executable and arguments that invoke the orchestrion CLI
// with args. A non-empty path (from Locate) runs that binary directly. An empty
// path falls back to `go run -mod=readonly github.com/DataDog/orchestrion`, so
// local development needs no orchestrion install.
//
// The fallback resolves and compiles the tool on every call. On a cold module
// cache that work can outlast a fixture timeout, which is why CI installs the
// binary instead of relying on the fallback.
func Command(path, goCommand string, args ...string) (string, []string) {
	if path != "" {
		return path, args
	}
	return goCommand, append([]string{"run", "-mod=readonly", "github.com/DataDog/orchestrion"}, args...)
}
