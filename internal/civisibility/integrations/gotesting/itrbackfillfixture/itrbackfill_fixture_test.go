// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package itrbackfillfixture

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestITRCoverageBackfillManualFixture(t *testing.T) {
	fixtureDir := filepath.Join("..", "fixtures", "itrbackfill", "manual")
	for _, test := range []struct {
		name     string
		extraEnv []string
	}{
		{name: "manual-count"},
		{name: "manual-codecoverage-disabled", extraEnv: []string{"DD_ITR_BACKFILL_CODE_COVERAGE=false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), test.name+".out")
			runFixtureCommand(t, fixtureDir, test.name, profile, test.extraEnv,
				"go", "test", "-mod=readonly", "./...",
				"-cover", "-covermode=count", "-coverpkg", "./...",
				"-count=1", "-coverprofile", profile)
			assertProfileBackfilled(t, profile, "fixtures/itrbackfill/manual/lib/lib.go")
		})
	}
}

func TestITRCoverageBackfillOrchestrionFixture(t *testing.T) {
	fixtureDir := filepath.Join("..", "fixtures", "itrbackfill", "orchestrion")
	assertOrchestrionFixtureDoesNotUseManualRunM(t, fixtureDir)

	for _, test := range []struct {
		name          string
		coverMode     string
		withProfile   bool
		extraEnv      []string
		extraTestArgs []string
	}{
		{name: "positive", coverMode: "count", withProfile: true},
		{name: "atomic", coverMode: "atomic", withProfile: true},
		{name: "no-coverprofile", coverMode: "count"},
		{name: "codecoverage-disabled", coverMode: "count", withProfile: true, extraEnv: []string{"DD_ITR_BACKFILL_CODE_COVERAGE=false"}},
		{name: "missing-line", coverMode: "count", withProfile: true},
		{name: "missing-coverage", coverMode: "count", withProfile: true},
		{name: "unmatched-coverage", coverMode: "count", withProfile: true},
		{name: "narrowing-run", coverMode: "count", withProfile: true, extraTestArgs: []string{"-run", "TestCoversLib"}},
		{name: "unsupported-set", coverMode: "set", withProfile: true},
		{name: "no-skippable", coverMode: "count", withProfile: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var profile string
			args := []string{
				"go", "run", "-mod=readonly", "github.com/DataDog/orchestrion",
				"go", "test", "-mod=readonly", "./...",
				"-cover", "-covermode=" + test.coverMode, "-coverpkg", "./...",
				"-count=1",
			}
			if test.withProfile {
				profile = filepath.Join(t.TempDir(), "orchestrion-"+test.name+".out")
				args = append(args, "-coverprofile", profile)
			}
			args = append(args, test.extraTestArgs...)
			runFixtureCommand(t, fixtureDir, test.name, profile, test.extraEnv, args...)
			if test.withProfile {
				assertProfileBackfilled(t, profile, "fixtures/itrbackfill/orchestrion/lib/lib.go")
			}
		})
	}
}

func runFixtureCommand(t *testing.T, fixtureDir, scenario, profile string, extraEnv []string, args ...string) {
	t.Helper()

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = fixtureDir
	cmd.Env = isolatedFixtureEnv(t, scenario)
	cmd.Env = append(cmd.Env, extraEnv...)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixture command failed: %v\n%s", err, output.String())
	}
	if profile != "" {
		if _, err := os.Stat(profile); err != nil {
			t.Fatalf("expected coverprofile %s: %v\n%s", profile, err, output.String())
		}
	}
}

func isolatedFixtureEnv(t *testing.T, scenario string) []string {
	t.Helper()

	tempRoot := t.TempDir()
	env := make([]string, 0, len(os.Environ())+18)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "DD_") ||
			strings.HasPrefix(key, "OTEL_") ||
			strings.HasPrefix(key, "CI") ||
			key == "GOFLAGS" ||
			key == "GOWORK" ||
			key == "HOME" ||
			key == "XDG_CACHE_HOME" ||
			key == "GOCACHE" {
			continue
		}
		env = append(env, item)
	}

	if !envHasKey(env, "GOMODCACHE") {
		env = append(env, "GOMODCACHE="+goEnv(t, "GOMODCACHE"))
	}

	env = append(env,
		"DD_ITR_BACKFILL_FIXTURE=1",
		"DD_ITR_BACKFILL_SCENARIO="+scenario,
		"DD_SERVICE=itr-backfill-"+scenario,
		"DD_ENV=itr-backfill-"+scenario,
		"HOME="+filepath.Join(tempRoot, "home"),
		"XDG_CACHE_HOME="+filepath.Join(tempRoot, "xdg"),
		"GOCACHE="+filepath.Join(tempRoot, "gocache"),
		"GOWORK=off",
		"GOFLAGS=",
	)
	return env
}

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func goEnv(t *testing.T, name string) string {
	t.Helper()

	output, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s failed: %v", name, err)
	}
	return strings.TrimSpace(string(output))
}

func assertProfileBackfilled(t *testing.T, profile, libPathContains string) {
	t.Helper()

	file, err := os.Open(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(filepath.ToSlash(line), libPathContains) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err == nil && count > 0 {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("expected %s to contain a backfilled positive count for %s", profile, libPathContains)
}

func assertOrchestrionFixtureDoesNotUseManualRunM(t *testing.T, fixtureDir string) {
	t.Helper()

	err := filepath.WalkDir(fixtureDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(contents)
		if strings.Contains(source, "gotesting.RunM") {
			t.Fatalf("Orchestrion fixture must use testing.M.Run instrumentation, not gotesting.RunM: %s", path)
		}
		if filepath.Base(path) != "orchestrion.tool.go" &&
			strings.Contains(source, "\"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting\"") {
			t.Fatalf("Orchestrion runtime fixture files must not import gotesting directly: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
