// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const setupGoAction = "../../.github/actions/setup-go/action.yml"

type actionStep struct {
	Name string `yaml:"name"`
	If   string `yaml:"if"`
	Run  string `yaml:"run"`
}

func setupGoStep(t *testing.T, name string) actionStep {
	t.Helper()
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
	for _, step := range action.Runs.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("setup-go has no step named %q", name)
	return actionStep{}
}

// runSetupGoStep executes a step's run block under bash with only the given
// environment, plus PATH, and returns GITHUB_OUTPUT, the combined output and
// the exit error.
func runSetupGoStep(t *testing.T, step actionStep, env map[string]string) (string, string, error) {
	t.Helper()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found: %v", tool, err)
		}
	}
	if strings.Contains(step.Run, "${{") {
		t.Fatalf("step %q interpolates expressions in its script; pass them through env", step.Name)
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "github_output")
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(step.Run), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "bash", script)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_OUTPUT=" + output}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	written, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(written), string(out), err
}

func TestSetupGoCacheProviderValidation(t *testing.T) {
	step := setupGoStep(t, "Validate cache provider")
	for _, provider := range []string{"cloudx", "github-cache"} {
		if _, out, err := runSetupGoStep(t, step, map[string]string{"CACHE_PROVIDER": provider}); err != nil {
			t.Errorf("provider %q rejected: %v\n%s", provider, err, out)
		}
	}
	for _, provider := range []string{"", "github", "CLOUDX", "cloudx "} {
		_, out, err := runSetupGoStep(t, step, map[string]string{"CACHE_PROVIDER": provider})
		if err == nil {
			t.Errorf("provider %q accepted", provider)
			continue
		}
		if !strings.Contains(out, "cache-provider must be 'cloudx' or 'github-cache'") {
			t.Errorf("provider %q: output lacks the clear error:\n%s", provider, out)
		}
	}
}

func TestSetupGoCloudxStepsSkippedForGitHubCache(t *testing.T) {
	for _, name := range []string{
		"Isolate and refresh Go build caches",
		"Detect restored module cache",
		"Verify the provider kept the selected toolchain",
	} {
		if cond := setupGoStep(t, name).If; !strings.Contains(cond, "inputs.cache-provider == 'cloudx'") {
			t.Errorf("step %q is not gated on the cloudx provider: if: %q", name, cond)
		}
	}
}

func observationRecord(t *testing.T, env map[string]string) map[string]any {
	t.Helper()
	written, out, err := runSetupGoStep(t, setupGoStep(t, "Record cache observations"), env)
	if err != nil {
		t.Fatalf("observation step failed: %v\n%s", err, out)
	}
	raw, ok := strings.CutPrefix(strings.TrimSpace(written), "cache-metrics=")
	if !ok {
		t.Fatalf("GITHUB_OUTPUT has no cache-metrics line: %q", written)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("cache-metrics is not JSON: %v\n%s", err, raw)
	}
	if !strings.Contains(out, "cache-observation:"+raw) {
		t.Errorf("job log lacks the cache-observation line:\n%s", out)
	}
	return record
}

func setupGoRestore(t *testing.T, record map[string]any) map[string]any {
	t.Helper()
	restores, _ := record["restores"].([]any)
	if len(restores) != 1 {
		t.Fatalf("want one restore entry, got %v", record["restores"])
	}
	entry, _ := restores[0].(map[string]any)
	if entry["name"] != "setup_go" {
		t.Fatalf("restore entry is not setup_go: %v", entry)
	}
	return entry
}

func TestSetupGoObservationGitHubCache(t *testing.T) {
	record := observationRecord(t, map[string]string{
		"PROVIDER":          "github-cache",
		"WORKLOAD":          "static-copyright",
		"REQUESTED_VERSION": "stable",
		"RESOLVED_VERSION":  "1.27.1",
		"CACHE_ENABLED":     "true",
		"SETUP_OUTCOME":     "success",
		"SETUP_CACHE_HIT":   "true",
		"RESTORED":          "",
	})
	if record["provider"] != "github-cache" {
		t.Errorf("provider = %v, want github-cache", record["provider"])
	}
	entry := setupGoRestore(t, record)
	if entry["cache_hit"] != "true" || entry["outcome"] != "success" {
		t.Errorf("setup_go entry lost the step's outputs: %v", entry)
	}
	if _, present := entry["restored"]; present {
		t.Errorf("github-cache entry carries restored: %v", entry)
	}
}

func TestSetupGoObservationCloudxRestored(t *testing.T) {
	base := map[string]string{
		"PROVIDER":          "cloudx",
		"WORKLOAD":          "unit-core",
		"REQUESTED_VERSION": "1.26",
		"RESOLVED_VERSION":  "1.26.8",
		"CACHE_ENABLED":     "true",
		"SETUP_OUTCOME":     "success",
		"SETUP_CACHE_HIT":   "false",
	}
	for _, restored := range []string{"true", "false"} {
		env := map[string]string{"RESTORED": restored}
		maps.Copy(env, base)
		record := observationRecord(t, env)
		if record["provider"] != "cloudx" {
			t.Errorf("provider = %v, want cloudx", record["provider"])
		}
		if got := setupGoRestore(t, record)["restored"]; got != restored {
			t.Errorf("restored = %v, want %q", got, restored)
		}
	}
	env := map[string]string{"RESTORED": ""}
	maps.Copy(env, base)
	if _, present := setupGoRestore(t, observationRecord(t, env))["restored"]; present {
		t.Error("restored present although the provider step produced no output")
	}
}
