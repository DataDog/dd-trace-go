// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexamplefixture

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	fixtureBuildTimeout = 5 * time.Minute
	fixtureRunTimeout   = 3 * time.Minute
)

var fixtureScenarios = []string{
	"pass",
	"fuzz-failure",
	"seed-lifecycle",
	"root-cleanup-goexit",
	"fuzz-missing-call",
	"example-mismatch",
	"example-panic",
	"example-panic-nil",
	"test-management",
	"active-fuzz",
	"filtered",
	"fatal-shutdown",
	"skip-lifecycle",
	"parallel-duration",
	"corpus-lifecycle",
	"repeat-run",
}

func TestFuzzAndExampleFixture(t *testing.T) {
	goCommand := fixtureGoCommand()
	goCache := filepath.Join(t.TempDir(), "gocache")
	goModCache := goEnv(t, "GOMODCACHE")
	for _, mode := range fixtureModes(goCommand) {
		// Compile each mode once. Re-running go test for every scenario repeats
		// Orchestrion weaving and can exhaust the package timeout on Windows.
		binaryName := "fuzzexample-" + mode + ".test"
		if runtime.GOOS == "windows" {
			binaryName += ".exe"
		}
		binaryPath := filepath.Join(t.TempDir(), binaryName)
		buildDir, buildArgs := fixtureBuildCommand(mode, binaryPath)
		runFixtureCommand(t, fixtureBuildTimeout, buildDir, fixtureEnv(t, mode, "build", goCache, goModCache), goCommand, buildArgs...)

		for _, scenario := range fixtureScenarios {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				runFixtureCommand(t, fixtureRunTimeout, fixtureRunDir(mode), fixtureEnv(t, mode, scenario, goCache, goModCache), binaryPath, fixtureScenarioArgs(scenario, filepath.Join(t.TempDir(), "fuzzcache"))...)
			})
		}
	}
}

func fixtureGoCommand() string {
	if command := os.Getenv("GO_CMD"); command != "" {
		return command
	}
	return "go"
}

func fixtureModes(goCommand string) []string {
	if filepath.Base(goCommand) == "gotip" || filepath.Base(goCommand) == "gotip.exe" {
		// Orchestrion does not yet support Go tip's -exportfd compiler flag:
		// https://github.com/DataDog/orchestrion/pull/899
		return []string{"manual"}
	}
	return []string{"manual", "orchestrion"}
}

func TestFixtureGoCommandAndModes(t *testing.T) {
	for _, command := range []string{"", "go", "gotip", filepath.Join("custom", "gotip"), "gotip.exe", filepath.Join("custom toolchain", "go")} {
		t.Run(command, func(t *testing.T) {
			t.Setenv("GO_CMD", command)
			wantCommand := command
			if wantCommand == "" {
				wantCommand = "go"
			}
			if got := fixtureGoCommand(); got != wantCommand {
				t.Fatalf("Go command = %q, want %q", got, wantCommand)
			}
			wantModes := []string{"manual", "orchestrion"}
			if filepath.Base(command) == "gotip" || filepath.Base(command) == "gotip.exe" {
				wantModes = []string{"manual"}
			}
			if got := fixtureModes(fixtureGoCommand()); !slices.Equal(got, wantModes) {
				t.Fatalf("fixture modes = %q, want %q", got, wantModes)
			}
		})
	}
}

func fixtureBuildCommand(mode, binaryPath string) (string, []string) {
	if mode == "orchestrion" {
		return filepath.Join("..", "fixtures", "itrbackfill", "orchestrion"), []string{
			"run", "-mod=readonly", "github.com/DataDog/orchestrion", "go", "test", "-c", "-mod=readonly",
			"-tags=fuzzexamplefixture", "-o", binaryPath, "./fuzzexample",
		}
	}
	return filepath.Join("..", "fixtures", "itrbackfill", "fuzzexample", "app"), []string{
		"test", "-c", "-mod=readonly", "-o", binaryPath, ".",
	}
}

func fixtureRunDir(mode string) string {
	if mode == "orchestrion" {
		return filepath.Join("..", "fixtures", "itrbackfill", "orchestrion", "fuzzexample")
	}
	return filepath.Join("..", "fixtures", "itrbackfill", "fuzzexample", "app")
}

func fixtureScenarioArgs(scenario, fuzzCacheDir string) []string {
	args := []string{"-test.count=1", "-test.timeout=2m"}
	switch scenario {
	case "corpus-lifecycle":
		return append(args, "-test.run=^TestFuzzCorpusLifecycle$", "-test.v=true")
	case "repeat-run":
		return append(args, "-test.run=^FuzzNativeParity$")
	case "fatal-shutdown":
		return append(args, "-test.run=^TestFuzzFatalShutdown$")
	case "skip-lifecycle":
		return append(args, "-test.run=^TestFuzzSkipLifecycle$")
	case "parallel-duration":
		return append(args, "-test.run=^TestFuzzParallelDuration$")
	case "seed-lifecycle":
		return append(args, "-test.run=^FuzzSeed(CleanupFailure|CleanupSkip|ParallelFailure)$")
	case "root-cleanup-goexit":
		return append(args, "-test.run=^FuzzRootCleanupGoexit$")
	case "fuzz-missing-call":
		return append(args, "-test.run=^FuzzMissingCall$")
	case "example-panic-nil":
		return append(args, "-test.run=^ExamplePanicNil$")
	case "test-management":
		return append(args, "-test.v=true", "-test.run=^(FuzzManaged|ExampleManaged)")
	case "active-fuzz":
		return append(args, "-test.run=^FuzzActiveOther$", "-test.fuzz=^FuzzNativeParity$", "-test.fuzztime=1x", "-test.fuzzcachedir="+fuzzCacheDir)
	case "filtered":
		return append(args, "-test.run=^TestNormalSelection$")
	default:
		return append(args, "-test.v=true", "-test.run=^(FuzzNative|ExampleNative)")
	}
}

func runFixtureCommand(t *testing.T, timeout time.Duration, dir string, env []string, executable string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("fixture command timed out after %s: %s\n%s", timeout, strings.Join(cmd.Args, " "), output.String())
		}
		t.Fatalf("fixture command failed: %s: %v\n%s", strings.Join(cmd.Args, " "), err, output.String())
	}
	if strings.Contains(output.String(), "CORPUS_MEMORY") {
		t.Log(output.String())
	}
}

func TestFixtureBuildCommands(t *testing.T) {
	binaryPath := filepath.Join("tmp", "fuzzexample.test")
	tests := []struct {
		mode     string
		wantDir  string
		wantArgs []string
	}{
		{
			mode:    "manual",
			wantDir: filepath.Join("..", "fixtures", "itrbackfill", "fuzzexample", "app"),
			wantArgs: []string{
				"test", "-c", "-mod=readonly", "-o", binaryPath, ".",
			},
		},
		{
			mode:    "orchestrion",
			wantDir: filepath.Join("..", "fixtures", "itrbackfill", "orchestrion"),
			wantArgs: []string{
				"run", "-mod=readonly", "github.com/DataDog/orchestrion", "go", "test", "-c", "-mod=readonly",
				"-tags=fuzzexamplefixture", "-o", binaryPath, "./fuzzexample",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			dir, args := fixtureBuildCommand(test.mode, binaryPath)
			if dir != test.wantDir {
				t.Errorf("build dir = %q, want %q", dir, test.wantDir)
			}
			if !slices.Equal(args, test.wantArgs) {
				t.Errorf("build args = %q, want %q", args, test.wantArgs)
			}
		})
	}
}

func TestFixtureScenarioArgs(t *testing.T) {
	common := []string{"-test.count=1", "-test.timeout=2m"}
	fuzzCacheDir := filepath.Join("tmp", "fuzzcache")
	tests := []struct {
		scenario string
		want     []string
	}{
		{scenario: "pass", want: append(slices.Clone(common), "-test.v=true", "-test.run=^(FuzzNative|ExampleNative)")},
		{scenario: "fuzz-failure", want: append(slices.Clone(common), "-test.v=true", "-test.run=^(FuzzNative|ExampleNative)")},
		{scenario: "seed-lifecycle", want: append(slices.Clone(common), "-test.run=^FuzzSeed(CleanupFailure|CleanupSkip|ParallelFailure)$")},
		{scenario: "root-cleanup-goexit", want: append(slices.Clone(common), "-test.run=^FuzzRootCleanupGoexit$")},
		{scenario: "fuzz-missing-call", want: append(slices.Clone(common), "-test.run=^FuzzMissingCall$")},
		{scenario: "example-mismatch", want: append(slices.Clone(common), "-test.v=true", "-test.run=^(FuzzNative|ExampleNative)")},
		{scenario: "example-panic", want: append(slices.Clone(common), "-test.v=true", "-test.run=^(FuzzNative|ExampleNative)")},
		{scenario: "example-panic-nil", want: append(slices.Clone(common), "-test.run=^ExamplePanicNil$")},
		{scenario: "test-management", want: append(slices.Clone(common), "-test.v=true", "-test.run=^(FuzzManaged|ExampleManaged)")},
		{scenario: "active-fuzz", want: append(slices.Clone(common), "-test.run=^FuzzActiveOther$", "-test.fuzz=^FuzzNativeParity$", "-test.fuzztime=1x", "-test.fuzzcachedir="+fuzzCacheDir)},
		{scenario: "filtered", want: append(slices.Clone(common), "-test.run=^TestNormalSelection$")},
		{scenario: "fatal-shutdown", want: append(slices.Clone(common), "-test.run=^TestFuzzFatalShutdown$")},
		{scenario: "skip-lifecycle", want: append(slices.Clone(common), "-test.run=^TestFuzzSkipLifecycle$")},
		{scenario: "parallel-duration", want: append(slices.Clone(common), "-test.run=^TestFuzzParallelDuration$")},
		{scenario: "corpus-lifecycle", want: append(slices.Clone(common), "-test.run=^TestFuzzCorpusLifecycle$", "-test.v=true")},
		{scenario: "repeat-run", want: append(slices.Clone(common), "-test.run=^FuzzNativeParity$")},
	}
	gotScenarios := make([]string, 0, len(tests))
	for _, test := range tests {
		gotScenarios = append(gotScenarios, test.scenario)
		t.Run(test.scenario, func(t *testing.T) {
			if got := fixtureScenarioArgs(test.scenario, fuzzCacheDir); !slices.Equal(got, test.want) {
				t.Errorf("scenario args = %q, want %q", got, test.want)
			}
		})
	}
	if !slices.Equal(gotScenarios, fixtureScenarios) {
		t.Errorf("tested scenarios = %q, want %q", gotScenarios, fixtureScenarios)
	}
}

func TestOrchestrionFuzzFixtureIsExcludedFromITRPackageList(t *testing.T) {
	fixtureDir := filepath.Join("..", "fixtures", "itrbackfill", "orchestrion")
	cmd := exec.Command(fixtureGoCommand(), "list", "-mod=readonly", "./...")
	cmd.Dir = fixtureDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list ITR fixture packages: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "/fuzzexample") {
		t.Fatalf("fuzz/example fixture leaked into the ITR package list:\n%s", output)
	}

	cmd = exec.Command(fixtureGoCommand(), "list", "-mod=readonly", "-tags=fuzzexamplefixture", "./fuzzexample")
	cmd.Dir = fixtureDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("list tagged fuzz/example fixture: %v\n%s", err, output)
	}
}

func fixtureEnv(t *testing.T, mode, scenario, goCache, goModCache string) []string {
	t.Helper()
	tempRoot := t.TempDir()
	env := make([]string, 0, len(os.Environ())+10)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "DD_") || strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "CI") ||
			key == "GOFLAGS" || key == "GOWORK" || (key == "HOME" && scenario != "build") || key == "XDG_CACHE_HOME" || key == "GOCACHE" || key == "GOMODCACHE" ||
			(scenario == "example-panic-nil" && key == "GODEBUG") {
			continue
		}
		env = append(env, item)
	}
	env = append(env,
		"DD_FUZZ_EXAMPLE_MODE="+mode,
		"DD_FUZZ_EXAMPLE_SCENARIO="+scenario,
		"DD_SERVICE=fuzz-example-"+mode,
		"DD_APPSEC_ENABLED=false",
		"DD_APPSEC_SCA_ENABLED=false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=false",
		"XDG_CACHE_HOME="+filepath.Join(tempRoot, "xdg"),
		"GOCACHE="+goCache,
		"GOMODCACHE="+goModCache,
		"GOWORK=off",
		"GOFLAGS=",
	)
	// Go command wrappers such as gotip locate their SDK under the real HOME.
	// Only the test binary needs an isolated home for CI Visibility state.
	if scenario != "build" {
		env = append(env, "HOME="+filepath.Join(tempRoot, "home"))
	}
	if scenario == "example-panic-nil" {
		env = append(env, "GODEBUG=panicnil=1")
	}
	return env
}

func goEnv(t *testing.T, name string) string {
	t.Helper()

	output, err := exec.Command(fixtureGoCommand(), "env", name).Output()
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

func TestFixtureEnvPreservesBuildHome(t *testing.T) {
	inheritedHome := filepath.Join(t.TempDir(), "sdk-home")
	t.Setenv("HOME", inheritedHome)
	for _, scenario := range []string{"build", "pass"} {
		t.Run(scenario, func(t *testing.T) {
			seen := 0
			for _, item := range fixtureEnv(t, "manual", scenario, "gocache", "gomodcache") {
				key, value, _ := strings.Cut(item, "=")
				if key != "HOME" {
					continue
				}
				seen++
				if (value == inheritedHome) != (scenario == "build") {
					t.Errorf("HOME = %q for %s; only builds must retain the inherited home", value, scenario)
				}
			}
			if seen != 1 {
				t.Errorf("HOME appears %d times, want exactly once", seen)
			}
		})
	}
}
