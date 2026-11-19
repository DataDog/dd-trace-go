// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

const regressionModeEnv = "_CRASHTRACKER_REGRESSION_MODE"

func TestSupervisorRegressionHelper(t *testing.T) {
	mode := os.Getenv(regressionModeEnv)
	if mode == "" {
		return
	}
	dir := os.Getenv("_CRASHTRACKER_REGRESSION_DIR")
	if mode == "activation" && os.Getenv(inheritedFDEnv) == "" {
		value := string(waitForFile(t, filepath.Join(dir, "listen-pid")))
		if err := os.Setenv("LISTEN_PID", value); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "reexec-fds" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(exe); err != nil {
			t.Fatal(err)
		}
		if err := crashtracker.Start(); err == nil || !strings.Contains(err.Error(), "start application process") {
			t.Fatalf("expected re-exec failure, got %v", err)
		}
		assertInheritedFile(t, 3)
		return
	}
	if err := crashtracker.Start(); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "activation":
		expected := os.Getenv("_CRASHTRACKER_EXPECT_LISTEN_PID")
		if expected == "self" {
			expected = strconv.Itoa(os.Getpid())
		}
		if got := os.Getenv("LISTEN_PID"); got != expected {
			t.Fatalf("LISTEN_PID = %q, want %q", got, expected)
		}
		if os.Getenv("LISTEN_FDS") != "1" || os.Getenv("LISTEN_FDNAMES") != "listener" {
			t.Fatal("socket activation metadata changed")
		}
		assertInheritedFile(t, 3)
		return
	case "listener":
		assertInheritedFile(t, 3)
		writeReady(t, dir)
		waitForFile(t, filepath.Join(dir, "exit"))
		return
	case "inherited-fds":
		assertInheritedFile(t, 3)
		assertInheritedFile(t, 5)
		panic("inherited descriptors survived")
	case "broken-stderr":
		if signal.Ignored(syscall.SIGPIPE) {
			t.Fatal("application inherited ignored SIGPIPE")
		}
		for range 150 {
			go func() { select {} }()
		}
		writeReady(t, dir)
		waitForFile(t, filepath.Join(dir, "crash"))
		panic("broken stderr crash")
	case "detached":
		pgid, err := syscall.Getpgid(os.Getppid())
		if err != nil {
			t.Fatal(err)
		}
		if syscall.Getpgrp() == pgid {
			t.Fatal("detached app shares the supervisor's process group")
		}
		return
	}
	t.Fatalf("unknown regression mode %q", mode)
}

func writeReady(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertInheritedFile(t *testing.T, fd int) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatalf("inherited fd %d: %v", fd, err)
	}
	got := fmt.Sprint(stat.Dev, ":", stat.Ino)
	if want := os.Getenv(fmt.Sprintf("_CRASHTRACKER_FD_%d", fd)); got != want {
		t.Fatalf("inherited fd %d identity = %s, want %s", fd, got, want)
	}
}

func supervisorRegressionCommand(t *testing.T, mode string) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorRegressionHelper$")
	detachSupervisor(cmd)
	cmd.Env = append(filterE2EEnv(os.Environ()), regressionModeEnv+"="+mode,
		"_CRASHTRACKER_REGRESSION_DIR="+dir, "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	output, err := os.Create(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	cmd.Stdout, cmd.Stderr = output, output
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if data, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return cmd, dir
}

func inheritedFile(t *testing.T, fd int, cmd *exec.Cmd) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "inherited")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("_CRASHTRACKER_FD_%d=%v:%v", fd, stat.Dev, stat.Ino))
	return f
}

func regressionOutput(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSupervisorPreservesInheritedDescriptors(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCrashReport(t, decompressGzipBody(t, r), "panic", "inherited descriptors survived")
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, dir := supervisorRegressionCommand(t, "inherited-fds")
	cmd.ExtraFiles = []*os.File{inheritedFile(t, 3, cmd), nil, inheritedFile(t, 5, cmd)}
	cmd.Env = append(cmd.Env, "DD_TRACE_AGENT_URL="+srv.URL)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 15*time.Second)
	select {
	case <-received:
	default:
		t.Fatalf("no report with inherited fd 3 and fd 5: %s", regressionOutput(t, dir))
	}
}

func TestSupervisorReexecFailurePreservesInheritedDescriptor(t *testing.T) {
	cmd, dir := supervisorRegressionCommand(t, "reexec-fds")
	cmd.ExtraFiles = []*os.File{inheritedFile(t, 3, cmd)}
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	path := filepath.Join(t.TempDir(), "helper")
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy helper: %v, close: %v", copyErr, closeErr)
	}
	cmd.Path, cmd.Args[0] = path, path
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("fail-open lost inherited fd: %v: %s", err, regressionOutput(t, dir))
	}
}

func TestSupervisorBrokenStderrStillUploads(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCrashReport(t, decompressGzipBody(t, r), "panic", "broken stderr crash")
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, dir := supervisorRegressionCommand(t, "broken-stderr")
	cmd.Env = append(cmd.Env, "DD_TRACE_AGENT_URL="+srv.URL, "GOTRACEBACK=all")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	waitForFile(t, filepath.Join(dir, "ready"))
	_ = r.Close()
	if err := os.WriteFile(filepath.Join(dir, "crash"), []byte("crash"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 15*time.Second)
	select {
	case <-received:
	default:
		t.Fatalf("broken stderr lost the report (status %v): %s", cmd.ProcessState, regressionOutput(t, dir))
	}
}

func TestSupervisorSocketActivation(t *testing.T) {
	for _, value := range []string{"supervisor", "unrelated"} {
		t.Run(value, func(t *testing.T) {
			cmd, dir := supervisorRegressionCommand(t, "activation")
			cmd.ExtraFiles = []*os.File{inheritedFile(t, 3, cmd)}
			expected := "self"
			if value == "unrelated" {
				expected = "1"
			}
			cmd.Env = append(cmd.Env, "_CRASHTRACKER_EXPECT_LISTEN_PID="+expected,
				"LISTEN_FDS=1", "LISTEN_FDNAMES=listener")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			listenPID := "1"
			if value == "supervisor" {
				listenPID = strconv.Itoa(cmd.Process.Pid)
			}
			if err := os.WriteFile(filepath.Join(dir, "listen-pid"), []byte(listenPID), 0600); err != nil {
				t.Fatal(err)
			}
			if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
				t.Fatalf("socket activation: %v: %s", err, regressionOutput(t, dir))
			}
		})
	}
}

func TestSupervisorDetachedProcessGroup(t *testing.T) {
	cmd, dir := supervisorRegressionCommand(t, "detached")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("detached process groups: %v: %s", err, strings.TrimSpace(regressionOutput(t, dir)))
	}
}
