// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	devDriveAction = "../../.github/actions/windows-dev-drive/action.yml"
	devDriveScript = "../../.github/actions/windows-dev-drive/dev-drive.ps1"
	devDriveStep   = "Provision Windows Dev Drive for Go caches"
)

// TestSetupGoDevDriveStepWiring pins the Dev Drive step inside the setup-go
// wrapper. The step must run before the toolchain selection, so both cache
// tiers (the cloudx provider and actions/setup-go's built-in cache) resolve
// and extract onto the Dev Drive, its run line must invoke the
// windows-dev-drive script shipped next to the wrapper, and that script
// must exist.
func TestSetupGoDevDriveStepWiring(t *testing.T) {
	raw, err := os.ReadFile(setupGoAction)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	steps := action.Runs.Steps
	at := func(name string) int {
		for i, step := range steps {
			if step.Name == name {
				return i
			}
		}
		return -1
	}
	devDrive := at(devDriveStep)
	if devDrive < 0 {
		t.Fatalf("setup-go has no step named %q", devDriveStep)
	}
	if got := steps[devDrive].If; got != "runner.os == 'Windows'" {
		t.Errorf("step %q is gated %q, want \"runner.os == 'Windows'\"", devDriveStep, got)
	}
	const wantRun = `& "$env:GITHUB_ACTION_PATH\..\windows-dev-drive\dev-drive.ps1"`
	if got := strings.TrimSpace(steps[devDrive].Run); got != wantRun {
		t.Errorf("step %q runs %q, want %q", devDriveStep, got, wantRun)
	}
	for _, before := range []string{"Select Go toolchain", "Isolate and refresh Go build caches"} {
		if other := at(before); other >= 0 && other < devDrive {
			t.Errorf("step %q runs after %q; it must come first so the caches resolve onto the Dev Drive", devDriveStep, before)
		} else if other < 0 {
			t.Errorf("setup-go has no step named %q (renamed?)", before)
		}
	}
	script := filepath.Join(filepath.Dir(setupGoAction), "..", "windows-dev-drive", "dev-drive.ps1")
	if _, err := os.Stat(script); err != nil {
		t.Errorf("the step's script does not resolve: %v", err)
	}
}

// TestDevDriveActionWiring checks the standalone action the appsec-style
// jobs use: gated on Windows, redirect input passed through the
// environment (the script's documented contract), no expression
// interpolation in the run line.
func TestDevDriveActionWiring(t *testing.T) {
	raw, err := os.ReadFile(devDriveAction)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Inputs struct {
			Redirect struct {
				Default string `yaml:"default"`
			} `yaml:"redirect"`
		} `yaml:"inputs"`
		Runs struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	if got := action.Inputs.Redirect.Default; got != "gocache,gomodcache" {
		t.Errorf("redirect input defaults to %q, want gocache,gomodcache", got)
	}
	if len(action.Runs.Steps) != 1 {
		t.Fatalf("want exactly one step, got %d", len(action.Runs.Steps))
	}
	step := action.Runs.Steps[0]
	if got := step.If; got != "runner.os == 'Windows'" {
		t.Errorf("the action step has condition %q, want \"runner.os == 'Windows'\"", got)
	}
	if step.Run == "" || strings.Contains(step.Run, "${{") {
		t.Errorf("run line must invoke the script without expression interpolation, got %q", step.Run)
	}
	if got := step.Env["DEV_DRIVE_REDIRECT"]; got != "${{ inputs.redirect }}" {
		t.Errorf("redirect input must reach the script through DEV_DRIVE_REDIRECT, got %q", got)
	}
}

// runDevDriveScript runs dev-drive.ps1 with the supplied environment and
// temporary GITHUB_ENV and GITHUB_OUTPUT files. It returns the combined
// output, the contents of both files, and the exit error. The runner
// invokes the script differently (dot-sourced under -command), so exit
// codes can differ from this invocation.
func runDevDriveScript(t *testing.T, env map[string]string) (string, string, string, error) {
	t.Helper()
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skipf("pwsh not found: %v (preinstalled on GitHub runners)", err)
	}
	dir := t.TempDir()
	envFile := filepath.Join(dir, "github_env")
	outFile := filepath.Join(dir, "github_output")
	for _, f := range []string{envFile, outFile} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script, err := filepath.Abs(devDriveScript)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), pwsh, "-NoProfile", "-File", script)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Env = append(cmd.Env,
		"GITHUB_ENV="+envFile,
		"GITHUB_OUTPUT="+outFile,
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	envWritten, rerr := os.ReadFile(envFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	outWritten, rerr := os.ReadFile(outFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(out), string(envWritten), string(outWritten), err
}

// TestDevDriveSkipsOffWindows pins the guard: the script exits cleanly and
// writes nothing on any other operating system — set or unset — which is
// what lets workflows invoke the step unconditionally.
func TestDevDriveSkipsOffWindows(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"linux": {"RUNNER_OS": "Linux"},
		// The runner always sets RUNNER_OS, so CI cannot reach the unset
		// case on its own; the contract promises it behaves like a non-
		// Windows value.
		"unset runner_os": {},
	} {
		t.Run(name, func(t *testing.T) {
			out, envWritten, outWritten, err := runDevDriveScript(t, env)
			if err != nil {
				t.Fatalf("script failed off Windows: %v\n%s", err, out)
			}
			if envWritten != "" || outWritten != "" {
				t.Errorf("off Windows the script must write nothing; GITHUB_ENV=%q GITHUB_OUTPUT=%q", envWritten, outWritten)
			}
		})
	}
}

// TestDevDriveFailOpen covers the provisioning catch: when no Dev Drive can
// be provisioned the script warns, exits successfully, and leaves the
// runner files untouched, so the caches stay on the default volume and the
// job continues. Skipped on Windows hosts, where provisioning can succeed
// and change the outcome.
func TestDevDriveFailOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("provisioning can succeed on Windows hosts; the fail-open path needs it to fail")
	}
	out, envWritten, outWritten, err := runDevDriveScript(t, map[string]string{"RUNNER_OS": "Windows"})
	if err != nil {
		t.Fatalf("provisioning failure must not fail the step: %v\n%s", err, out)
	}
	if !strings.Contains(out, "::warning::Dev Drive provisioning failed") {
		t.Errorf("output lacks the provisioning warning:\n%s", out)
	}
	if envWritten != "" || outWritten != "" {
		t.Errorf("a failed provisioning must write nothing; GITHUB_ENV=%q GITHUB_OUTPUT=%q", envWritten, outWritten)
	}
}

// TestDevDriveRedirectsCaches runs the Windows path against a directory
// root (the script's DEV_DRIVE_ROOT seam), so the whole flow — validation,
// directory creation, env and output emission — runs without a real
// Windows volume. The full assignments and the dev-drive output are
// compared, not just the variable names, so a wrong root or a missing
// separator fails the test.
func TestDevDriveRedirectsCaches(t *testing.T) {
	// The runs below stay green on Linux CI runners and macOS dev machines.
	// The default case leaves DEV_DRIVE_REDIRECT unset, pinning that the
	// script redirects both caches without any caller configuration.
	for name, tc := range map[string]struct {
		redirect string
		want     []string
	}{
		"default both":    {want: []string{"gocache", "gomodcache"}},
		"gocache only":    {redirect: "gocache", want: []string{"gocache"}},
		"gomodcache only": {redirect: "gomodcache", want: []string{"gomodcache"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			env := map[string]string{"RUNNER_OS": "Windows", "DEV_DRIVE_ROOT": root}
			if tc.redirect != "" {
				env["DEV_DRIVE_REDIRECT"] = tc.redirect
			}
			out, envWritten, outWritten, err := runDevDriveScript(t, env)
			if err != nil {
				t.Fatalf("script failed: %v\n%s", err, out)
			}
			wantLines := make([]string, 0, len(tc.want))
			for _, cache := range tc.want {
				variable := "GOCACHE"
				if cache == "gomodcache" {
					variable = "GOMODCACHE"
				}
				wantLines = append(wantLines, variable+"="+filepath.Join(root, cache))
			}
			if got := strings.Split(strings.TrimSpace(envWritten), "\n"); !slices.Equal(got, wantLines) {
				t.Errorf("GITHUB_ENV lines %q, want %q", got, wantLines)
			}
			if want := "dev-drive=" + root; strings.TrimSpace(outWritten) != want {
				t.Errorf("GITHUB_OUTPUT %q, want %q", outWritten, want)
			}
		})
	}
}

// TestDevDriveRejectsBadConfiguration makes configuration errors fail the
// script loudly: an unknown cache name (a typo would silently keep one
// cache on the default volume), a redirect with no cache names at all
// (which would report success while moving nothing), and a line break in
// the root override (which could inject extra GITHUB_ENV entries through
// the emitted paths).
func TestDevDriveRejectsBadConfiguration(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"unknown cache name": {
			env:  map[string]string{"DEV_DRIVE_REDIRECT": "gocache,buildcache"},
			want: "unknown cache name 'buildcache'",
		},
		"no cache names": {
			env:  map[string]string{"DEV_DRIVE_REDIRECT": " ,, "},
			want: "must name at least one cache",
		},
		"line break in root": {
			env:  map[string]string{"DEV_DRIVE_ROOT": "E:\r\nGITHUB_ENV=evil"},
			want: "must not contain line breaks",
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"RUNNER_OS": "Windows", "DEV_DRIVE_ROOT": t.TempDir()}
			maps.Copy(env, tc.env)
			out, _, _, err := runDevDriveScript(t, env)
			if err == nil {
				t.Fatalf("bad configuration was accepted")
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}
