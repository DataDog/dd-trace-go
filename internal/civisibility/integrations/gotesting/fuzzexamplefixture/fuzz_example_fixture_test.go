// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexamplefixture

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzAndExampleFixture(t *testing.T) {
	goCache := filepath.Join(t.TempDir(), "gocache")
	goModCache := goEnv(t, "GOMODCACHE")
	for _, mode := range []string{"manual", "orchestrion"} {
		for _, scenario := range []string{
			"pass",
			"fuzz-failure",
			"seed-lifecycle",
			"fuzz-missing-call",
			"example-mismatch",
			"example-panic",
			"example-panic-nil",
			"active-fuzz",
			"filtered",
		} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				fixtureDir := filepath.Join("..", "fixtures", "itrbackfill", "fuzzexample", "app")
				args := []string{"test", "-mod=readonly", "-count=1", "-v", "-run", "^(FuzzNative|ExampleNative)"}
				switch scenario {
				case "seed-lifecycle":
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^FuzzSeed(CleanupFailure|CleanupSkip|ParallelFailure)$"}
				case "fuzz-missing-call":
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^FuzzMissingCall$"}
				case "example-panic-nil":
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^ExamplePanicNil$"}
				case "active-fuzz":
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^FuzzActiveOther$", "-fuzz", "^FuzzNativeParity$", "-fuzztime", "1x"}
				case "filtered":
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^TestNormalSelection$"}
				}
				if mode == "orchestrion" {
					fixtureDir = filepath.Join("..", "fixtures", "itrbackfill", "orchestrion")
					args = append([]string{"run", "-mod=readonly", "github.com/DataDog/orchestrion", "go"}, args...)
					args = append(args, "./fuzzexample")
				}
				cmd := exec.Command("go", args...)
				cmd.Dir = fixtureDir
				cmd.Env = fixtureEnv(t, mode, scenario, goCache, goModCache)
				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				if err := cmd.Run(); err != nil {
					t.Fatalf("fixture failed: %v\n%s", err, output.String())
				}
			})
		}
	}
}

func fixtureEnv(t *testing.T, mode, scenario, goCache, goModCache string) []string {
	t.Helper()
	tempRoot := t.TempDir()
	env := make([]string, 0, len(os.Environ())+10)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "CI") ||
			key == "GOFLAGS" || key == "GOWORK" || key == "HOME" || key == "XDG_CACHE_HOME" || key == "GOCACHE" || key == "GOMODCACHE" ||
			(scenario == "example-panic-nil" && key == "GODEBUG") {
			continue
		}
		env = append(env, item)
	}
	env = append(env,
		"DD_FUZZ_EXAMPLE_MODE="+mode,
		"DD_FUZZ_EXAMPLE_SCENARIO="+scenario,
		"DD_SERVICE=fuzz-example-"+mode,
		"HOME="+filepath.Join(tempRoot, "home"),
		"XDG_CACHE_HOME="+filepath.Join(tempRoot, "xdg"),
		"GOCACHE="+goCache,
		"GOMODCACHE="+goModCache,
		"GOWORK=off",
		"GOFLAGS=",
	)
	if scenario == "example-panic-nil" {
		env = append(env, "GODEBUG=panicnil=1")
	}
	return env
}

func goEnv(t *testing.T, name string) string {
	t.Helper()

	output, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s failed: %v", name, err)
	}
	return strings.TrimSpace(string(output))
}

func TestFixtureEnvUsesExplicitGoCaches(t *testing.T) {
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "inherited-gocache"))
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "inherited-gomodcache"))

	goCache := filepath.Join(t.TempDir(), "gocache")
	goModCache := filepath.Join(t.TempDir(), "gomodcache")
	env := fixtureEnv(t, "manual", "pass", goCache, goModCache)

	want := map[string]string{
		"GOCACHE":    goCache,
		"GOMODCACHE": goModCache,
	}
	seen := make(map[string]int, len(want))
	for _, item := range env {
		key, value, _ := strings.Cut(item, "=")
		if _, ok := want[key]; !ok {
			continue
		}
		seen[key]++
		if value != want[key] {
			t.Errorf("%s = %q, want %q", key, value, want[key])
		}
	}
	for key := range want {
		if seen[key] != 1 {
			t.Errorf("%s appears %d times, want exactly once", key, seen[key])
		}
	}
}
