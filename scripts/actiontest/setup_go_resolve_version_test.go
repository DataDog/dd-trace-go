// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const resolveVersionScript = "../../.github/actions/setup-go/resolve-version.sh"

// runResolveVersion executes the resolver with a stub `go` whose GOVERSION
// is the fixture, and returns the go-version-key it wrote to GITHUB_OUTPUT
// (or its combined output and error when it fails).
func runResolveVersion(t *testing.T, goversion string) (string, string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not found: %v", err)
	}
	dir := t.TempDir()
	// A stub go binary: `go env GOVERSION` prints the fixture; anything a
	// well-behaved `go` must not print goes to stderr so it cannot corrupt
	// the resolver's output.
	stub := filepath.Join(dir, "go")
	// /bin/bash, not /usr/bin/env bash: the stub runs with a PATH that
	// contains only this directory, so env cannot find bash.
	script := "#!/bin/bash\n" +
		"if [[ \"${1:-}\" == \"env\" && \"${2:-}\" == \"GOVERSION\" ]]; then\n" +
		"  printf '%s\\n' " + "'" + goversion + "'" + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unsupported stub call: $*\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "github_output")
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "bash", resolveVersionScript)
	cmd.Env = []string{
		"PATH=" + dir,
		"GITHUB_OUTPUT=" + output,
	}
	out, err := cmd.CombinedOutput()
	written, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	key, ok := strings.CutPrefix(strings.TrimSpace(string(written)), "go-version-key=")
	if !ok {
		key = ""
	}
	return key, string(out), err
}

// TestResolveVersionMapsEverySpellingToTheMinor covers the one invariant the
// cache-key scheme rests on: every way actions/setup-go can report an
// installed STABLE toolchain resolves to the same key for the same Go
// minor, and patch releases change nothing. Prerelease toolchains key on
// their exact semver: the provider's nested installer resolves go-version
// as a range, and a semver range cannot match a prerelease.
func TestResolveVersionMapsEverySpellingToTheMinor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		goversion  string
		want       string
		wantErr    bool
		wantErrOut string
	}{
		{name: "patched release", goversion: "go1.27.4", want: "1.27"},
		{name: "minor without patch", goversion: "go1.27", want: "1.27"},
		{name: "older supported minor", goversion: "go1.26.11", want: "1.26"},
		{name: "release candidate keys on its exact semver", goversion: "go1.28rc1", want: "1.28.0-rc.1"},
		{name: "beta keys on its exact semver", goversion: "go1.28beta2", want: "1.28.0-beta.2"},
		{
			name:       "devel toolchain is rejected",
			goversion:  "devel go1.28.0-abcdef01",
			wantErr:    true,
			wantErrOut: "cannot derive a cache-key version from GOVERSION",
		},
		{
			name:       "a version without the go prefix is rejected",
			goversion:  "1.27.4",
			wantErr:    true,
			wantErrOut: "cannot derive a cache-key version from GOVERSION",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, out, err := runResolveVersion(t, tc.goversion)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("GOVERSION %q resolved to %q, want failure", tc.goversion, key)
				}
				if !strings.Contains(out, tc.wantErrOut) {
					t.Fatalf("error output %q lacks %q", out, tc.wantErrOut)
				}
				return
			}
			if err != nil {
				t.Fatalf("GOVERSION %q failed: %v\n%s", tc.goversion, err, out)
			}
			if key != tc.want {
				t.Fatalf("GOVERSION %q resolved to %q, want %q", tc.goversion, key, tc.want)
			}
		})
	}
}
