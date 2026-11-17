// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product contains software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"os"
	"os/exec"
	"path/filepath"
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
// and extract onto the Dev Drive, and its run line must point at the
// windows-dev-drive script shipped next to the wrapper.
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
		t.Errorf("step is gated %q, want \"runner.os == 'Windows'\", so callers can invoke the action unconditionally", got)
	}
	if step.Run == "" || strings.Contains(step.Run, "${{") {
		t.Errorf("run line must invoke the script without expression interpolation, got %q", step.Run)
	}
	if got := step.Env["DEV_DRIVE_REDIRECT"]; got != "${{ inputs.redirect }}" {
		t.Errorf("redirect input must reach the script through DEV_DRIVE_REDIRECT, got %q", got)
	}
}

// runDevDriveScript executes dev-drive.ps1 the way the runner does, with the
// given environment plus temp GITHUB_ENV/GITHUB_OUTPUT files, and returns the
// exit error, the combined output, and the contents of both files.
func runDevDriveScript(t *testing.T, env map[string]string) (error, string, string, string) {
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
		"GITHUB_WORKSPACE="+filepath.Join(dir, "workspace"),
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
	return err, string(out), string(envWritten), string(outWritten)
}

// TestDevDriveSkipsOffWindows pins the guard: the script exits cleanly and
// writes nothing on any other operating system, which is what lets workflows
// invoke the step unconditionally.
func TestDevDriveSkipsOffWindows(t *testing.T) {
	err, out, envWritten, outWritten := runDevDriveScript(t, map[string]string{"RUNNER_OS": "Linux"})
	if err != nil {
		t.Fatalf("script failed off Windows: %v\n%s", err, out)
	}
	if envWritten != "" || outWritten != "" {
		t.Errorf("off Windows the script must write nothing; GITHUB_ENV=%q GITHUB_OUTPUT=%q", envWritten, outWritten)
	}
}

// TestDevDriveRedirectsCaches runs the Windows path against a directory
// root (the script's DEV_DRIVE_ROOT seam), so the whole flow — directory
// creation, env and output emission — runs without a real Windows volume.
// Off Windows the emitted paths carry the platform separator; only the
// variable assignment order is asserted.
func TestDevDriveRedirectsCaches(t *testing.T) {
	// The runs below stay green on Linux CI runners and macOS dev machines.
	// The default case leaves DEV_DRIVE_REDIRECT unset, pinning that the
	// script redirects both caches without any caller configuration.
	for name, tc := range map[string]struct {
		redirect string
		want     []string
	}{
		"default both":    {want: []string{"GOCACHE=", "GOMODCACHE="}},
		"gocache only":    {redirect: "gocache", want: []string{"GOCACHE="}},
		"gomodcache only": {redirect: "gomodcache", want: []string{"GOMODCACHE="}},
	} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"RUNNER_OS": "Windows", "DEV_DRIVE_ROOT": t.TempDir()}
			if tc.redirect != "" {
				env["DEV_DRIVE_REDIRECT"] = tc.redirect
			}
			err, out, envWritten, outWritten := runDevDriveScript(t, env)
			if err != nil {
				t.Fatalf("script failed: %v\n%s", err, out)
			}
			var redirected []string
			for line := range strings.SplitSeq(strings.TrimSpace(envWritten), "\n") {
				if strings.HasPrefix(line, "GOCACHE=") || strings.HasPrefix(line, "GOMODCACHE=") {
					redirected = append(redirected, strings.SplitN(line, "=", 2)[0]+"=")
				}
			}
			if !slices.Equal(redirected, tc.want) {
				t.Errorf("redirected %v, want %v (GITHUB_ENV %q)", redirected, tc.want, envWritten)
			}
			if !strings.Contains(outWritten, "dev-drive=") {
				t.Errorf("GITHUB_OUTPUT missing the dev-drive line, got %q", outWritten)
			}
		})
	}
}

// TestDevDriveRejectsUnknownCache makes a redirect typo fail the script
// loudly instead of silently keeping one cache on the default volume.
func TestDevDriveRejectsUnknownCache(t *testing.T) {
	err, out, _, _ := runDevDriveScript(t, map[string]string{
		"RUNNER_OS":          "Windows",
		"DEV_DRIVE_ROOT":     t.TempDir(),
		"DEV_DRIVE_REDIRECT": "gocache,buildcache",
	})
	if err == nil {
		t.Fatalf("unknown cache name was accepted")
	}
	if !strings.Contains(out, "unknown cache name 'buildcache'") {
		t.Errorf("output lacks the clear error:\n%s", out)
	}
}
