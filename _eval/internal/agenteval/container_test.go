// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
)

func requireContainerTests(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENT_EVAL_DOCKER_TEST") != "1" {
		t.Skip("set AGENT_EVAL_DOCKER_TEST=1 to run container integration tests")
	}
	if runtime.GOOS == "windows" {
		t.Skip("container runner currently targets Linux containers")
	}
}

func TestAgentContainerName(t *testing.T) {
	name := agentContainerName("Codex", "/tmp/results/selfcheck-abc/baseline/valkey-option-conventions/1")
	wantPrefix := "agent-eval-codex-selfcheck-abc-baseline-valkey-option-conventions-1-"
	if !strings.HasPrefix(name, wantPrefix) {
		t.Fatalf("container name = %q, want prefix %q", name, wantPrefix)
	}
	if len(name) > 128 {
		t.Fatalf("container name has %d characters, want at most 128", len(name))
	}
}

func TestContainerResourceLimits(t *testing.T) {
	tests := []struct {
		name       string
		resources  ContainerResources
		wantCPUs   int64
		wantMemory int64
	}{
		{name: "defaults", wantCPUs: 4_000_000_000, wantMemory: 4 << 30},
		{name: "configured", resources: ContainerResources{CPUs: 3, MemoryGiB: 5}, wantCPUs: 3_000_000_000, wantMemory: 5 << 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCPUs, gotMemory := tt.resources.limits()
			if gotCPUs != tt.wantCPUs || gotMemory != tt.wantMemory {
				t.Fatalf("limits() = (%d, %d), want (%d, %d)", gotCPUs, gotMemory, tt.wantCPUs, tt.wantMemory)
			}
		})
	}
}

func TestAgentContainerIsolation(t *testing.T) {
	requireContainerTests(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.work"), []byte("go 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	transcript := filepath.Join(artifacts, "transcript")
	stderr := filepath.Join(artifacts, "stderr")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := runInContainer(ctx, workspace, transcript, stderr, containerInvocation{
		Image: "dd-trace-go-agent-eval/codex:0.147.0",
		Command: []string{"sh", "-c", `
			set -eu
			test "$HOME" = /tmp/agent-home
			test -z "${GOWORK:-}"
			test "$(go env GOWORK)" = /workspace/go.work
			test ! -e /Users/rodrigo.arguello
			test -z "${DD_API_KEY:-}"
			if touch /root/must-not-write 2>/dev/null; then exit 91; fi
			printf 'isolated\n' > /workspace/result.txt
			printf 'ok\n'
		`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		body, _ := os.ReadFile(stderr)
		t.Fatalf("exit code %d: %s", res.ExitCode, body)
	}
	body, err := os.ReadFile(filepath.Join(workspace, "result.txt"))
	if err != nil || string(body) != "isolated\n" {
		t.Fatalf("workspace output = %q, %v", body, err)
	}
	info, err := os.Stat(filepath.Join(workspace, "result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		t.Errorf("workspace file uid = %d, want %d", stat.Uid, os.Getuid())
	}
}

func TestAgentContainerTimeoutKeepsLogs(t *testing.T) {
	requireContainerTests(t)
	workspace := t.TempDir()
	artifacts := t.TempDir()
	transcript := filepath.Join(artifacts, "transcript")
	stderr := filepath.Join(artifacts, "stderr")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := runInContainer(ctx, workspace, transcript, stderr, containerInvocation{
		Name:    agentContainerName("timeout-test", artifacts),
		Image:   "dd-trace-go-agent-eval/codex:0.147.0",
		Command: []string{"sh", "-c", "printf 'before-timeout\\n'; sleep 30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, exit code %d", res.ExitCode)
	}
	body, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "before-timeout") {
		t.Fatalf("transcript = %q, want partial output", body)
	}
}

func TestClaudeContainerTrustsWorkspace(t *testing.T) {
	requireContainerTests(t)
	runContainerCheck(t, containerInvocation{
		Image: "dd-trace-go-agent-eval/claude:2.1.231",
		Command: []string{"sh", "-c",
			`jq -e '.projects["/workspace"].hasTrustDialogAccepted == true' "$HOME/.claude.json" && printf 'trusted\n'`},
	}, "trusted")
}

func TestAgentContainerAuthentication(t *testing.T) {
	requireContainerTests(t)
	t.Run("claude", func(t *testing.T) {
		env, err := claudeContainerAuth(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		runContainerCheck(t, containerInvocation{
			Image:       "dd-trace-go-agent-eval/claude:2.1.231",
			Command:     []string{"claude", "auth", "status", "--json"},
			Environment: env,
		}, `"loggedIn": true`)
	})
	t.Run("codex", func(t *testing.T) {
		auth, err := codexAuthFile()
		if err != nil {
			t.Fatal(err)
		}
		runContainerCheck(t, containerInvocation{
			Image:   "dd-trace-go-agent-eval/codex:0.147.0",
			Command: []string{"codex", "login", "status"},
			Environment: map[string]string{
				"CODEX_HOME": containerHome + "/.codex",
			},
			AuthMounts: []mount.Mount{{
				Type: mount.TypeBind, Source: auth, Target: "/run/agent-auth/codex-auth.json", ReadOnly: true,
			}},
		}, "Logged in")
	})
}

func runContainerCheck(t *testing.T, inv containerInvocation, want string) {
	t.Helper()
	workspace := t.TempDir()
	artifacts := t.TempDir()
	transcript := filepath.Join(artifacts, "transcript")
	stderr := filepath.Join(artifacts, "stderr")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := runInContainer(ctx, workspace, transcript, stderr, inv)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := os.ReadFile(transcript)
	stderrBody, _ := os.ReadFile(stderr)
	combined := string(stdout) + string(stderrBody)
	if res.ExitCode != 0 || !strings.Contains(combined, want) {
		t.Fatalf("exit code %d, output %q, want %q", res.ExitCode, combined, want)
	}
}
