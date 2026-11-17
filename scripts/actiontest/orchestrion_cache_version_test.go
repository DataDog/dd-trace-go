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
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/modfile"
)

const (
	orchestrionWorkflow  = "../../.github/workflows/orchestrion.yml"
	cacheVersionStepName = "Resolve Orchestrion cache version"
	integrationGoMod     = "../../internal/orchestrion/_integration/go.mod"
)

// versionAwkPattern captures the awk program of the step's
// version="$(awk '...' go.mod)" line.
var versionAwkPattern = regexp.MustCompile(`version="\$\(awk '([^']*)' `)

// cacheVersionAwk returns the awk program the workflow uses to read the
// Orchestrion version from go.mod, so the test cannot drift from it.
func cacheVersionAwk(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(orchestrionWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Name != cacheVersionStepName {
				continue
			}
			match := versionAwkPattern.FindStringSubmatch(step.Run)
			if match == nil {
				t.Fatalf("step %q has no version=\"$(awk '...' ...)\" line", cacheVersionStepName)
			}
			return match[1]
		}
	}
	t.Fatalf("orchestrion.yml has no step named %q", cacheVersionStepName)
	return ""
}

func runVersionAwk(t *testing.T, program, goMod string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "awk", program, goMod)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("awk failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestOrchestrionCacheVersion(t *testing.T) {
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skipf("awk not found: %v", err)
	}
	program := cacheVersionAwk(t)

	for _, tc := range []struct {
		name  string
		goMod string
		want  string
	}{
		{
			name:  "versioned block replacement first",
			goMod: "replace (\n\tgithub.com/DataDog/orchestrion v1.1.0 => github.com/fork/orchestrion v2.0.0\n)\n\nrequire (\n\tgithub.com/DataDog/orchestrion v1.2.0\n)\n",
			want:  "v1.2.0",
		},
		{
			name:  "unversioned block replacement first",
			goMod: "replace (\n\tgithub.com/DataDog/orchestrion => github.com/fork/orchestrion v2.0.0\n)\n\nrequire (\n\tgithub.com/DataDog/orchestrion v1.2.0\n)\n",
			want:  "v1.2.0",
		},
		{
			name:  "local directory replacement first",
			goMod: "replace github.com/DataDog/orchestrion v1.1.0 => ../local\n\nrequire (\n\tgithub.com/DataDog/orchestrion v1.2.0 // indirect\n)\n",
			want:  "v1.2.0",
		},
		{
			name:  "single-line requirement",
			goMod: "module example.com/m\n\nrequire github.com/DataDog/orchestrion v1.2.0\n",
			want:  "v1.2.0",
		},
		{
			name:  "replacement only",
			goMod: "replace github.com/DataDog/orchestrion => ../local\n",
			want:  "",
		},
		{
			name:  "requirement first",
			goMod: "require (\n\tgithub.com/DataDog/orchestrion v1.2.0\n)\n\nreplace github.com/DataDog/orchestrion v1.2.0 => ../local\n",
			want:  "v1.2.0",
		},
		{
			name:  "requirement only",
			goMod: "require (\n\tgithub.com/DataDog/orchestrion v1.2.0\n)\n",
			want:  "v1.2.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go.mod")
			if err := os.WriteFile(path, []byte(tc.goMod), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := runVersionAwk(t, program, path); got != tc.want {
				t.Errorf("version = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("integration module", func(t *testing.T) {
		raw, err := os.ReadFile(integrationGoMod)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := modfile.Parse(integrationGoMod, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		var want string
		for _, req := range mod.Require {
			if req.Mod.Path == "github.com/DataDog/orchestrion" {
				want = req.Mod.Version
			}
		}
		if want == "" {
			t.Fatal("integration go.mod does not require github.com/DataDog/orchestrion")
		}
		if got := runVersionAwk(t, program, integrationGoMod); got != want {
			t.Errorf("version = %q, want %q", got, want)
		}
	})
}
