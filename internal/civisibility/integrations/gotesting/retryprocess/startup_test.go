// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	processRetryStartupFixtureEnv        = "PROCESS_RETRY_STARTUP_FIXTURE"
	processRetryStartupRerunFileEnv      = "PROCESS_RETRY_STARTUP_RERUN_FILE"
	processRetryStartupConflictFileEnv   = "PROCESS_RETRY_STARTUP_CONFLICT_FILE"
	processRetryStartupConflictMarkerEnv = "PROCESS_RETRY_STARTUP_CONFLICT_MARKER_FILE"
)

var (
	startupRerunRuns    atomic.Int32
	startupConflictRuns atomic.Int32
	startupConflictFile *os.File
)

func init() {
	if path := processRetryFixtureEnv(processRetryStartupRerunFileEnv); path != "" {
		appendStartupFixtureLine(path, "init")
	}
	if path := processRetryFixtureEnv(processRetryStartupConflictFileEnv); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if processRetryFixtureChild() {
				appendStartupFixtureLine(processRetryFixtureEnv(processRetryStartupConflictMarkerEnv), "child_conflict")
			} else {
				appendStartupFixtureLine(processRetryFixtureEnv(processRetryStartupConflictMarkerEnv), "parent_conflict")
			}
			return
		}
		startupConflictFile = file
	}
}

func TestProcessRetryStartupRerunsController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "startup")
	path := filepathForStartupFixture(t, "startup-reruns")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryStartupRerunsParent$", "-test.v")
	cmd.Env = processRetryScenarioEnvironment(
		processRetryStartupFixtureEnv+"=true",
		processRetryStartupRerunFileEnv+"="+path,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup-rerun subprocess failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 || lines[0] != "init" || lines[1] != "init" {
		t.Fatalf("expected exactly one parent and one child package init event, got %q", lines)
	}
}

func TestProcessRetryStartupConflictController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "startup conflict")
	resourcePath := filepathForStartupFixture(t, "startup-conflict-resource")
	markerPath := filepathForStartupFixture(t, "startup-conflict-marker")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryStartupConflictParent$", "-test.v")
	cmd.Env = processRetryScenarioEnvironment(
		processRetryStartupFixtureEnv+"=true",
		processRetryStartupConflictFileEnv+"="+resourcePath,
		processRetryStartupConflictMarkerEnv+"="+markerPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup-conflict subprocess failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 1 || lines[0] != "child_conflict" {
		t.Fatalf("expected exactly one child conflict and no parent conflicts, got %q", lines)
	}
}

func TestProcessRetryStartupRerunsParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("startup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if startupRerunRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent startup count: %d", startupRerunRuns.Load())
		}
		return
	}
	if startupRerunRuns.Add(1) == 1 {
		t.Fatal("first startup-rerun execution must fail to trigger process retry")
	}
	t.Fatalf("startup-rerun retry ran in the parent process with run count %d", startupRerunRuns.Load())
}

func TestProcessRetryStartupConflictParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("startup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if startupConflictRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent startup conflict count: %d", startupConflictRuns.Load())
		}
		return
	}
	if startupConflictRuns.Add(1) == 1 {
		t.Fatal("first startup-conflict execution must fail to trigger process retry")
	}
	t.Fatalf("startup-conflict retry ran in the parent process with run count %d", startupConflictRuns.Load())
}

func filepathForStartupFixture(t *testing.T, name string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), name+"-*")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendStartupFixtureLine(path, line string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line + "\n")
}
