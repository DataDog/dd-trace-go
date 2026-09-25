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
	for _, mode := range []string{"manual", "orchestrion"} {
		for _, scenario := range []string{"pass", "fuzz-failure", "example-mismatch", "example-panic", "active-fuzz", "filtered"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				fixtureDir := filepath.Join("..", "fixtures", "itrbackfill", "fuzzexample", "app")
				args := []string{"test", "-mod=readonly", "-count=1", "-v", "-run", "^(FuzzNative|ExampleNative)"}
				if scenario == "active-fuzz" {
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^$", "-fuzz", "^FuzzNativeParity$", "-fuzztime", "1x"}
				} else if scenario == "filtered" {
					args = []string{"test", "-mod=readonly", "-count=1", "-run", "^TestNormalSelection$"}
				}
				if mode == "orchestrion" {
					fixtureDir = filepath.Join("..", "fixtures", "itrbackfill", "orchestrion")
					args = append([]string{"run", "-mod=readonly", "github.com/DataDog/orchestrion", "go"}, args...)
					args = append(args, "./fuzzexample")
				}
				cmd := exec.Command("go", args...)
				cmd.Dir = fixtureDir
				cmd.Env = fixtureEnv(t, mode, scenario, goCache)
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

func fixtureEnv(t *testing.T, mode, scenario, goCache string) []string {
	t.Helper()
	tempRoot := t.TempDir()
	env := make([]string, 0, len(os.Environ())+10)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "CI") ||
			key == "GOFLAGS" || key == "GOWORK" || key == "HOME" || key == "XDG_CACHE_HOME" || key == "GOCACHE" {
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
		"GOWORK=off",
		"GOFLAGS=",
	)
	return env
}
