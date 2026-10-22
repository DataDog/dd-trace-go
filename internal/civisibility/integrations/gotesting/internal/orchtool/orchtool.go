// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package orchtool resolves the orchestrion CLI for the fixture tests that
// drive it in subprocesses.
package orchtool

import "os/exec"

// Locate returns the path of an orchestrion binary on PATH. It returns the
// empty string when none is installed. CI installs the orchestrion version
// pinned by the orchestrion fixture module to GOPATH/bin before the tests run.
func Locate() string {
	path, err := exec.LookPath("orchestrion")
	if err != nil {
		return ""
	}
	return path
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
