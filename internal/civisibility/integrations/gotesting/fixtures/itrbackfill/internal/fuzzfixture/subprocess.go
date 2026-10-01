// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzfixture

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

// CheckCorpusLifecycle keeps the intake in the parent while the child runs its
// native corpus, with and without instrumentation. Entry points and t.Run stay
// in each fixture package so Orchestrion still weaves the real workload.
func CheckCorpusLifecycle(t *testing.T, enabled string, corpusSize int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server := mockci.Start(net.SettingsResponseData{SubtestFeaturesEnabled: true}, nil, nil)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^FuzzCorpus$", "-test.timeout=25s")
	skippedCorpus := enabled == "skip"
	if skippedCorpus {
		enabled = "true"
	}
	cmd.Env = append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=corpus-child", "DD_CIVISIBILITY_ENABLED="+enabled)
	if skippedCorpus {
		cmd.Env = append(cmd.Env, "DD_FUZZ_CORPUS_SKIP=true")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("corpus failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "CORPUS_MEMORY") {
		t.Fatalf("missing memory sample: %s", output)
	}
	t.Logf("enabled=%s %s", enabled, output)
	want := 0
	if enabled == "true" {
		want = corpusSize + 1
	}
	if got := server.EventTypeCount(constants.SpanTypeTest); got != want {
		t.Fatalf("native test count = %d, want %d", got, want)
	}
	for _, event := range server.Events() {
		status := constants.TestStatusPass
		if skippedCorpus && strings.Contains(event.Content.Meta[constants.TestName], "/") {
			status = constants.TestStatusSkip
		}
		if event.Type == constants.SpanTypeTest && event.Content.Meta[constants.TestStatus] != status {
			t.Fatalf("unexpected corpus outcome: %+v", event)
		}
	}
}

// FatalScenarios is shared by the subprocess matrix and its event expectations.
func FatalScenarios() []string {
	return []string{"root", "seed", "managed-root", "managed-seed", "seed-cleanup", "managed-seed-cleanup", "seed-body-cleanup", "managed-seed-body-cleanup",
		"root-body-error", "managed-root-body-error", "root-body-fatal", "managed-root-body-fatal",
		"seed-body-error", "managed-seed-body-error", "seed-body-fatal", "managed-seed-body-fatal"}
}

// CheckFatalShutdown keeps the intake alive when native testing aborts the
// child. target is the fixture's real Fuzz declaration, not a helper callback,
// so Test Management uses that package's module, suite, and root identity.
func CheckFatalShutdown(t *testing.T, scenario string, target func(*testing.F)) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := runtime.FuncForPC(reflect.ValueOf(target).Pointer())
	root := source.Name()[strings.LastIndex(source.Name(), ".")+1:]
	managed := strings.HasPrefix(scenario, "managed-")
	settings := net.SettingsResponseData{SubtestFeaturesEnabled: true}
	var management *net.TestManagementTestsResponseDataModules
	name := root
	if strings.Contains(scenario, "seed") {
		name += "/seed#0"
	}
	if managed {
		settings.TestManagement.Enabled = true
		module, suite := utils.GetModuleAndSuiteName(source.Entry())
		management = &net.TestManagementTestsResponseDataModules{Modules: map[string]net.TestManagementTestsResponseDataSuites{
			module: {Suites: map[string]net.TestManagementTestsResponseDataTests{
				suite: {Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
					name: {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
				}},
			}},
		}}
	}
	server := mockci.StartWithTestManagement(settings, nil, nil, management)
	defer server.Close()
	args := []string{"-test.run=^" + root + "$", "-test.timeout=15s"}
	childEnv := append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=fatal-child", "DD_FUZZ_FATAL_KIND="+scenario)
	if strings.HasSuffix(scenario, "-error") || strings.HasSuffix(scenario, "-fatal") {
		// Check the uninstrumented process too: cleanup Fatal replaces the
		// panic with a normal failure, while cleanup Error leaves it fatal.
		nativeOutput, nativeErr := runFixtureChild(binary, append(childEnv, "DD_CIVISIBILITY_ENABLED=false"), args...)
		wantExit := 2
		if strings.HasSuffix(scenario, "-fatal") {
			wantExit = 1
		}
		if exit, ok := nativeErr.(*exec.ExitError); !ok || exit.ExitCode() != wantExit {
			t.Fatalf("disabled CI exit=%v, want %d: %s", nativeErr, wantExit, nativeOutput)
		}
		if server.EventTypeCount(constants.SpanTypeTest) != 0 {
			t.Fatal("disabled CI emitted fatal test events")
		}
	}
	output, err := runFixtureChild(binary, childEnv, args...)
	if (err == nil) != managed {
		t.Fatalf("exit = %v; output: %s", err, output)
	}
	if !managed {
		exit, ok := err.(*exec.ExitError)
		wantExit := 2
		if strings.HasSuffix(scenario, "-fatal") {
			wantExit = 1 // cleanup Fatal suppresses the body panic in native testing
		}
		if !ok || exit.ExitCode() != wantExit {
			t.Fatalf("panic exit = %v, want %d: %s", err, wantExit, output)
		}
	}
	continues := managed || strings.HasSuffix(scenario, "-fatal")
	if strings.Contains(string(output), "NEXT_SEED_EXECUTED") != (continues && strings.Contains(scenario, "seed")) {
		t.Fatalf("unexpected later seed: %s", output)
	}
	wantMessage := "root panic sentinel"
	if strings.Contains(scenario, "body-") {
		wantMessage = "root body panic sentinel"
		if strings.Contains(scenario, "seed") {
			wantMessage = "seed body panic sentinel"
		}
	} else if strings.Contains(scenario, "cleanup") {
		wantMessage = "seed cleanup panic sentinel"
	} else if strings.Contains(scenario, "seed") {
		wantMessage = "seed panic sentinel"
	}
	if !server.HasEventResourceSuffixMeta(name, ext.ErrorType, "panic") ||
		!server.HasEventResourceSuffixMeta(name, ext.ErrorMsg, wantMessage) ||
		!server.HasEventResourceSuffixMetaKey(name, ext.ErrorStack) {
		for _, event := range server.Events() {
			if event.Type == constants.SpanTypeTest && strings.HasSuffix(event.Content.Resource, "."+name) {
				t.Logf("panic type=%q message=%q stack=%q", event.Content.Meta[ext.ErrorType], event.Content.Meta[ext.ErrorMsg], event.Content.Meta[ext.ErrorStack])
			}
		}
		t.Fatalf("panic details not flushed for %s", name)
	}
	if strings.HasSuffix(scenario, "-error") || strings.HasSuffix(scenario, "-fatal") {
		for _, event := range server.Events() {
			if event.Type == constants.SpanTypeTest && strings.HasSuffix(event.Content.Resource, "."+name) &&
				strings.Contains(event.Content.Meta[ext.ErrorStack], "runTestCleanupCallbacks") {
				t.Fatalf("cleanup stack replaced the body panic stack: %+v", event)
			}
		}
	}
	if !server.HasEventResourceSuffixMeta(name, constants.TestStatus, constants.TestStatusFail) {
		t.Fatalf("failure not flushed: %+v", server.Events())
	}
	final := constants.TestStatusFail
	if managed {
		final = constants.TestStatusSkip
	}
	if !server.HasEventResourceSuffixMeta(name, constants.TestFinalStatus, final) {
		t.Fatalf("wrong final status: %+v", server.Events())
	}
	rootStatus := constants.TestStatusFail
	if managed && strings.Contains(scenario, "seed") {
		rootStatus = constants.TestStatusPass
	}
	if !server.HasEventResourceSuffixMeta(root, constants.TestStatus, rootStatus) {
		t.Fatalf("root not flushed: %+v", server.Events())
	}
	count := 1
	if strings.Contains(scenario, "seed") {
		count = 2
	}
	if continues && strings.Contains(scenario, "seed") {
		count = 3
	}
	if got := server.EventTypeCount(constants.SpanTypeTest); got != count {
		t.Fatalf("test events = %d, want %d: %+v", got, count, server.Events())
	}
	for _, kind := range []string{constants.SpanTypeTestSession, constants.SpanTypeTestModule, constants.SpanTypeTestSuite} {
		if got := server.EventTypeCount(kind); got != 1 {
			t.Fatalf("%s count = %d, want 1: %+v", kind, got, server.Events())
		}
	}
}

// SkipScenarios exercises the woven skip hooks independently: a cleanup may
// still fail or panic after any of these methods has terminated the body.
func SkipScenarios() []string {
	scenarios := make([]string, 0, 28)
	for _, target := range []string{"root", "seed"} {
		for _, method := range []string{"skip", "skipf", "skipnow"} {
			for _, cleanup := range []string{"pass", "error", "fatal", "panic"} {
				scenarios = append(scenarios, target+"-"+method+"-"+cleanup)
			}
		}
		scenarios = append(scenarios, target+"-panic-skipnow", target+"-panic-goexit")
	}
	return scenarios
}

// CheckSkipLifecycle compares the actual native process result with CI on/off,
// then checks the deferred event's final status and skip reason. Exact duration
// and closure ordering are checked independently with virtual time and channels.
func CheckSkipLifecycle(t *testing.T, scenario, mode string, target func(*testing.F)) {
	t.Helper()
	root := fuzzTargetName(target)
	parts := strings.Split(scenario, "-")
	for _, enabled := range []string{"false", "true"} {
		server := mockci.Start(net.SettingsResponseData{SubtestFeaturesEnabled: true}, nil, nil)
		output, exit := runLifecycleChild(t, root, enabled, "DD_FUZZ_SKIP_METHOD="+parts[1], "DD_FUZZ_SKIP_CLEANUP="+parts[2])
		wantExit := 1
		status := constants.TestStatusFail
		if parts[2] == "pass" || parts[2] == "skipnow" {
			wantExit, status = 0, constants.TestStatusSkip
		} else if parts[2] == "panic" || parts[2] == "goexit" {
			wantExit = 2
		}
		if exit != wantExit || !strings.Contains(output, "SKIP_CLEANUP_EXECUTED") {
			server.Close()
			t.Fatalf("CI=%s skip exit=%d, want %d: %s", enabled, exit, wantExit, output)
		}
		if enabled == "false" {
			if server.EventTypeCount(constants.SpanTypeTest) != 0 {
				t.Fatal("disabled CI emitted test events")
			}
			server.Close()
			continue
		}
		name, count := root, 1
		if parts[0] == "seed" {
			name, count = root+"/seed#0", 2
		}
		if server.EventTypeCount(constants.SpanTypeTest) != count ||
			!server.HasEventResourceSuffixMeta(name, constants.TestStatus, status) ||
			!server.HasEventResourceSuffixMeta(name, constants.TestFinalStatus, status) {
			t.Fatalf("skip cleanup events: %+v", server.Events())
		}
		for _, event := range server.Events() {
			if event.Type != constants.SpanTypeTest || !strings.HasSuffix(event.Content.Resource, "."+name) {
				continue
			}
			if mode == "orchestrion" && (parts[1] == "skip" || parts[1] == "skipf") && event.Content.Meta[constants.TestSkipReason] != "body skip sentinel" {
				t.Fatalf("skip reason lost: %+v", event)
			}
		}
		server.Close()
	}
}

// CheckParallelDuration verifies that root cleanup follows both parallel seeds
// and payload durations match Go's own printed result, not a wall-clock budget.
// Exact wait subtraction is tested separately using synctest.
func CheckParallelDuration(t *testing.T, target func(*testing.F)) {
	t.Helper()
	root := fuzzTargetName(target)
	for _, enabled := range []string{"false", "true"} {
		server := mockci.Start(net.SettingsResponseData{SubtestFeaturesEnabled: true}, nil, nil)
		output, exit := runLifecycleChild(t, root, enabled)
		if exit != 0 || !strings.Contains(output, "PARALLEL_ROOT_CLEANUP_EXECUTED") {
			t.Fatalf("CI=%s parallel test failed: %s", enabled, output)
		}
		for _, name := range []string{root, root + "/seed#0", root + "/seed#1"} {
			printedDuration(t, output, name)
		}
		if enabled == "true" {
			for _, event := range server.Events() {
				if event.Type == constants.SpanTypeTest {
					name := event.Content.Meta[constants.TestName]
					printed := printedDuration(t, output, name)
					if reported := fmt.Sprintf("%.2f", time.Duration(event.Content.Duration).Seconds()); reported != printed {
						t.Fatalf("payload duration differs from Go: %+v, printed %s", event, printed)
					}
				}
			}
			if got := server.EventTypeCount(constants.SpanTypeTest); got != 3 {
				t.Fatalf("parallel events=%d, want root and two seeds", got)
			}
		} else if server.EventTypeCount(constants.SpanTypeTest) != 0 {
			t.Fatal("disabled CI emitted parallel test events")
		}
		server.Close()
	}
}

func fuzzTargetName(target func(*testing.F)) string {
	name := runtime.FuncForPC(reflect.ValueOf(target).Pointer()).Name()
	return name[strings.LastIndex(name, ".")+1:]
}

func printedDuration(t *testing.T, output, name string) string {
	t.Helper()
	match := regexp.MustCompile(`--- PASS: ` + regexp.QuoteMeta(name) + ` \(([0-9.]+)s\)`).FindStringSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("missing Go duration for %s: %s", name, output)
	}
	return match[1]
}

func runLifecycleChild(t *testing.T, root, enabled string, extraEnv ...string) (string, int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	childEnv := append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=lifecycle-child", "DD_CIVISIBILITY_ENABLED="+enabled)
	childEnv = append(childEnv, extraEnv...)
	output, err := runFixtureChild(binary, childEnv, "-test.run=^"+root+"$", "-test.v=true", "-test.parallel=1", "-test.timeout=15s")
	if err == nil {
		return string(output), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(output), exit.ExitCode()
	}
	t.Fatalf("lifecycle process: %v: %s", err, output)
	return "", -1
}

// Each invocation owns its deadline: the CI-disabled reference run must not
// consume the CI-enabled child's budget.
func runFixtureChild(binary string, childEnv []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = childEnv
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := runChildCommand(ctx, cmd)
	return output.Bytes(), err
}

func runChildCommand(ctx context.Context, cmd *exec.Cmd) error {
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		// Let the Go runtime print goroutine stacks before forced termination.
		return cmd.Process.Signal(syscall.SIGQUIT)
	}
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		// A killed process can have the expected failure exit code on Windows;
		// cancellation must never count as a successful lifecycle assertion.
		return fmt.Errorf("fixture command %q: %w (process error: %v)", cmd.Args, ctx.Err(), err)
	}
	return err
}
