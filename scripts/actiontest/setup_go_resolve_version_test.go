// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const resolveVersionScript = "../../.github/actions/setup-go/resolve-version.sh"

// resolveVersionOutputs holds the resolver's GITHUB_OUTPUT fields.
type resolveVersionOutputs struct {
	key      string // go-version-key: the cache-key version class
	provider string // go-version-provider: what the cloudx provider gets
}

// runResolveVersion executes the resolver with a stub `go` whose GOVERSION
// is the fixture and REQUESTED_VERSION as the caller's go-version input,
// and returns both outputs (or the combined output and error when the
// script fails).
func runResolveVersion(t *testing.T, requested, goversion string) (resolveVersionOutputs, string, error) {
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
		"REQUESTED_VERSION=" + requested,
		"GITHUB_OUTPUT=" + output,
	}
	out, err := cmd.CombinedOutput()
	written, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(written)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		fields[k] = v
	}
	return resolveVersionOutputs{key: fields["go-version-key"], provider: fields["go-version-provider"]}, string(out), err
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
			got, out, err := runResolveVersion(t, "", tc.goversion)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("GOVERSION %q resolved to %+v, want failure", tc.goversion, got)
				}
				if !strings.Contains(out, tc.wantErrOut) {
					t.Fatalf("error output %q lacks %q", out, tc.wantErrOut)
				}
				return
			}
			if err != nil {
				t.Fatalf("GOVERSION %q failed: %v\n%s", tc.goversion, err, out)
			}
			if got.key != tc.want {
				t.Fatalf("GOVERSION %q resolved to key %q, want %q", tc.goversion, got.key, tc.want)
			}
		})
	}
}

// TestResolveVersionProviderPreservesExactPatches covers the provider
// output: every alias and minor spelling passes the version class, so the
// nested installer finds the binary select-go installed and the cache key
// carries one entry per minor. A fully-specified request ("1.27.1")
// passes its exact patch instead: check-latest never upgrades a full
// version, so the minor would let the nested installer resolve the range
// to the latest patch and swap the toolchain under the Verify step.
func TestResolveVersionProviderPreservesExactPatches(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested string
		goversion string
		wantKey   string
		wantProv  string
	}{
		{
			name:      "alias request passes the class",
			requested: "stable", goversion: "go1.27.4",
			wantKey: "1.27", wantProv: "1.27",
		},
		{
			name:      "moving alias request passes the class",
			requested: "oldstable", goversion: "go1.26.11",
			wantKey: "1.26", wantProv: "1.26",
		},
		{
			name:      "minor request passes the class",
			requested: "1.27", goversion: "go1.27.4",
			wantKey: "1.27", wantProv: "1.27",
		},
		{
			name:      "exact patch request passes its exact patch",
			requested: "1.27.1", goversion: "go1.27.1",
			wantKey: "1.27", wantProv: "1.27.1",
		},
		{
			name:      "equals-constrained request passes its exact patch",
			requested: "=1.27.1", goversion: "go1.27.1",
			wantKey: "1.27", wantProv: "1.27.1",
		},
		{
			name:      "v-prefixed request passes its exact patch",
			requested: "v1.27.1", goversion: "go1.27.1",
			wantKey: "1.27", wantProv: "1.27.1",
		},
		{
			name:      "padded request passes its exact patch",
			requested: "1.27.01", goversion: "go1.27.1",
			wantKey: "1.27", wantProv: "1.27.1",
		},
		{
			name:      "prerelease request passes its exact semver",
			requested: "1.28.0-rc.1", goversion: "go1.28rc1",
			wantKey: "1.28.0-rc.1", wantProv: "1.28.0-rc.1",
		},
		{
			name:      "empty request passes the class",
			requested: "", goversion: "go1.27.4",
			wantKey: "1.27", wantProv: "1.27",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, out, err := runResolveVersion(t, tc.requested, tc.goversion)
			if err != nil {
				t.Fatalf("requested %q GOVERSION %q failed: %v\n%s", tc.requested, tc.goversion, err, out)
			}
			if got.key != tc.wantKey || got.provider != tc.wantProv {
				t.Fatalf("requested %q GOVERSION %q resolved to key=%q provider=%q, want key=%q provider=%q",
					tc.requested, tc.goversion, got.key, got.provider, tc.wantKey, tc.wantProv)
			}
		})
	}
}

// TestResolveStepPassesRequestedVersion pins the resolve step's env: the
// script reads REQUESTED_VERSION to separate unconstrained spellings
// from exact ones. If the env line is dropped, the script sees an empty
// request, cloudx gets the class for every spelling, and the
// exact-patch failure returns while every other test still passes.
func TestResolveStepPassesRequestedVersion(t *testing.T) {
	step := setupGoStep(t, "Resolve cache-key version")
	const want = "${{ inputs.go-version }}"
	if got := step.Env["REQUESTED_VERSION"]; got != want {
		t.Fatalf("REQUESTED_VERSION = %q, want %q", got, want)
	}
}

// TestActionStepOutputReferencesAreDeclared walks every
// steps.<id>.outputs.* reference in the action's run blocks and env
// values, and checks that an earlier step in the same action declares
// that id. A renamed step id silently breaks wiring that actionlint
// cannot see (it does not lint composite action files).
func TestActionStepOutputReferencesAreDeclared(t *testing.T) {
	raw, err := os.ReadFile(setupGoAction)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				ID   string            `yaml:"id"`
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				With map[string]string `yaml:"with"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	refRe := regexp.MustCompile(`steps\.([A-Za-z0-9_-]+)\.outputs\.[A-Za-z0-9_-]+`)
	seen := map[string]bool{}
	for _, step := range action.Runs.Steps {
		if step.ID != "" {
			seen[step.ID] = true
		}
		for _, text := range append(
			collectStrings(step.Env),
			collectStrings(step.With)...,
		) {
			text += "\n" + step.Run
			for _, m := range refRe.FindAllStringSubmatch(text, -1) {
				id := m[1]
				if !seen[id] {
					t.Errorf("step %q references outputs of step id %q, which no earlier step declares",
						step.Name, id)
				}
			}
		}
	}
}

// collectStrings returns the map's values in a stable order.
func collectStrings(m map[string]string) []string {
	vals := make([]string, 0, len(m))
	for _, v := range m {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	return vals
}
