// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package orchtool resolves the orchestrion CLI for the fixture tests that
// drive it in subprocesses.
package orchtool

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Locate returns the path of an orchestrion binary on PATH that reports the
// version required by the module at moduleDir. It returns the empty string
// when no such binary is installed.
//
// A binary with a different version refuses to instrument the module and
// exits with status 255, so an unrelated install (a workflow's binary, a
// developer's local one) must not shadow the pinned version.
func Locate(goCommand, moduleDir string) string {
	path, err := exec.LookPath("orchestrion")
	if err != nil {
		return ""
	}
	want, err := ModuleVersion(goCommand, moduleDir)
	if err != nil {
		return ""
	}
	got, err := BinaryVersion(path)
	if err != nil || got != want {
		return ""
	}
	return path
}

// ModuleVersion returns the orchestrion version that the module at moduleDir
// requires. The module's go.mod pins the version.
func ModuleVersion(goCommand, moduleDir string) (string, error) {
	cmd := exec.Command(goCommand, "list", "-m", "-f", "{{.Version}}", "github.com/DataDog/orchestrion")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	version := parseVersion(output)
	if version == "" {
		return "", fmt.Errorf("no orchestrion version in %s", moduleDir)
	}
	return version, nil
}

// BinaryVersion returns the version that the orchestrion binary at path
// reports for itself.
func BinaryVersion(path string) (string, error) {
	output, err := exec.Command(path, "version").Output()
	if err != nil {
		return "", err
	}
	version := parseVersion(output)
	if version == "" {
		return "", fmt.Errorf("no orchestrion version in the output of %s version", path)
	}
	return version, nil
}

// parseVersion returns the leading version token in orchestrion output, such
// as "v1.9.0" in "orchestrion v1.9.0". It returns the empty string when the
// output holds none.
func parseVersion(output []byte) string {
	for field := range strings.FieldsSeq(string(output)) {
		if len(field) > 1 && field[0] == 'v' && field[1] >= '0' && field[1] <= '9' {
			return field
		}
	}
	return ""
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
