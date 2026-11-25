// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// startStderrVictim starts the application role under a supervisor. The caller
// owns the stderr of the supervisor.
func startStderrVictim(t *testing.T, role, agentURL string, stderr *os.File, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v=false")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()),
		e2eRoleEnv+"="+role,
		"DD_TRACE_AGENT_URL="+agentURL,
		"DD_CRASHTRACKING_ENABLED=true",
	)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdout, cmd.Stderr = nil, stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn %s: %v", role, err)
	}
	return cmd
}

func TestSupervisorTeeKeepsStderrBytes(t *testing.T) {
	stderrPath := filepath.Join(t.TempDir(), "stderr")
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	const size = 1 << 20
	cmd := startStderrVictim(t, "stderr-pattern", "http://127.0.0.1:1", stderr, e2eStderrBytesEnv+"="+strconv.Itoa(size))
	_ = waitForVictim(t, cmd, 30*time.Second)
	if code := cmd.ProcessState.ExitCode(); code != stderrPatternExitCode {
		t.Fatalf("exit code = %d, want %d", code, stderrPatternExitCode)
	}
	got, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := stderrPattern(size); !bytes.Equal(got, want) {
		t.Fatalf("stderr has %d bytes that differ from the %d bytes that the application wrote", len(got), len(want))
	}
}

func TestSupervisorTeeDoesNotBlockApplicationWhenStderrIsBroken(t *testing.T) {
	goFile := filepath.Join(t.TempDir(), "go")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	const size = 10 << 20
	cmd := startStderrVictim(t, "stderr-pattern", "http://127.0.0.1:1", w, e2eGoFileEnv+"="+goFile, e2eStderrBytesEnv+"="+strconv.Itoa(size))
	// Break the stderr of the supervisor before the application writes.
	_ = w.Close()
	_ = r.Close()
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 30*time.Second)
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if status.Signaled() || status.ExitStatus() != stderrPatternExitCode {
		t.Fatalf("supervisor status = %v, want exit status %d", status, stderrPatternExitCode)
	}
}

func TestSupervisorReportsFatalErrorMessage(t *testing.T) {
	for _, tt := range []struct {
		role        string
		wantType    string
		wantMessage string
		wantStderr  string
	}{
		{"crash-map-writes", "runtime.plainError", "concurrent map writes", "fatal error: concurrent map writes"},
		{"crash-map-writes-large-stderr", "runtime.plainError", "concurrent map writes", "fatal error: concurrent map writes"},
		{"crash-mutex-unlock", "runtime.plainError", "sync: unlock of unlocked mutex", "fatal error: sync: unlock of unlocked mutex"},
		{"crash-rwmutex-runlock", "runtime.plainError", "sync: RUnlock of unlocked RWMutex", "fatal error: sync: RUnlock of unlocked RWMutex"},
		{"crash-stack-overflow", "runtime.plainError", "stack overflow (goroutine stack exceeds 32768-byte limit)", "fatal error: stack overflow"},
		{"crash-nil-deref", "SIGSEGV", "runtime error: invalid memory address or nil pointer dereference", "panic: runtime error: invalid memory address or nil pointer dereference"},
		{"crash-repanic", "panic", "repanic after: first", "panic: first [recovered"},
	} {
		t.Run(tt.role, func(t *testing.T) {
			received := make(chan []byte, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case received <- decompressGzipBody(t, r):
				default:
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()
			stderrPath := filepath.Join(t.TempDir(), "stderr")
			stderr, err := os.Create(stderrPath)
			if err != nil {
				t.Fatal(err)
			}
			defer stderr.Close()
			cmd := startStderrVictim(t, tt.role, srv.URL, stderr)
			_ = waitForVictim(t, cmd, 30*time.Second)
			select {
			case body := <-received:
				var report struct {
					Error struct{ Type, Message string }
				}
				if err := json.Unmarshal(body, &report); err != nil {
					t.Fatal(err)
				}
				if report.Error.Type != tt.wantType || report.Error.Message != tt.wantMessage {
					t.Errorf("error = (%q, %q), want (%q, %q)", report.Error.Type, report.Error.Message, tt.wantType, tt.wantMessage)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("timeout waiting for crash report")
			}
			got, err := os.ReadFile(stderrPath)
			if err != nil {
				t.Fatal(err)
			}
			if tt.role == "crash-map-writes-large-stderr" {
				if len(got) < 10<<20 {
					t.Errorf("stderr has %d bytes, want at least 10 MiB", len(got))
				}
				if count := bytes.Count(got, []byte(tt.wantStderr)); count != 1 {
					t.Errorf("stderr has %d fatal headers, want 1", count)
				}
				t.Logf("stderr has %d bytes", len(got))
			}
			if !bytes.Contains(got, []byte(tt.wantStderr)) {
				t.Errorf("stderr does not contain %q: %.500s", tt.wantStderr, got)
			}
		})
	}
}
