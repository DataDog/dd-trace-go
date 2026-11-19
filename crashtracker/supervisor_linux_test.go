// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorDeathKillsChild(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "wait")
	pid, err := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
	if err != nil {
		t.Fatal(err)
	}
	// Check that the child stays alive before the supervisor dies.
	// This does not force the spawning OS thread to retire.
	time.Sleep(100 * time.Millisecond)
	if childIsDead(t, pid) {
		t.Fatal("child died while its supervisor was still alive")
	}
	start := time.Now()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, time.Second)
	for !childIsDead(t, pid) {
		if time.Since(start) >= time.Second {
			t.Fatal("child survived supervisor SIGKILL for 1 second")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("child died within %s of supervisor SIGKILL", time.Since(start))
}

func TestSupervisorWaitFailure(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "wait-error")
	_ = waitForVictim(t, cmd, 5*time.Second)
	if cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("supervisor exit code = %d, want 1", cmd.ProcessState.ExitCode())
	}
	output, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "crashtracker: wait for application process failed:") {
		t.Fatalf("missing wait failure warning: %s", output)
	}
}

func TestSupervisorWaitFailureStillUploadsCrashReport(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	cmd, dir := startSupervisorHelper(t, "wait-error-panic", "DD_TRACE_AGENT_URL="+srv.URL)
	_ = waitForVictim(t, cmd, 10*time.Second)
	output, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	if got := cmd.ProcessState.ExitCode(); got != 1 {
		t.Fatalf("supervisor exit code = %d, want 1: %s", got, output)
	}
	if !strings.Contains(string(output), "crashtracker: wait for application process failed:") {
		t.Fatalf("missing wait failure warning: %s", output)
	}
	select {
	case <-received:
	default:
		t.Fatalf("wait failure lost the crash report: %s", output)
	}
}

func TestSupervisorClosesInheritedListener(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	identity, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	cmd, dir := supervisorRegressionCommand(t, "listener")
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = append(cmd.Env, fmt.Sprintf("_CRASHTRACKER_FD_3=%v:%v", stat.Dev, stat.Ino))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	_ = listener.Close()
	child := string(waitForFile(t, filepath.Join(dir, "ready")))
	childIdentity, err := os.Readlink("/proc/" + child + "/fd/3")
	if err != nil || childIdentity != identity {
		t.Fatalf("child fd 3 = %q, error = %v, want %q", childIdentity, err, identity)
	}
	probe, err := net.ListenTCP("tcp4", listener.Addr().(*net.TCPAddr))
	if err == nil {
		_ = probe.Close()
		t.Fatal("inherited listener is no longer bound")
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("probe inherited listener: %v", err)
	}
	fdDir := fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if link == identity {
			t.Fatalf("supervisor still holds inherited listener at fd %s", entry.Name())
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte("exit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("listener helper: %v: %s", err, regressionOutput(t, dir))
	}
}

func childIsDead(t *testing.T, pid int) bool {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	// A zombie has exited even if the container's PID 1 has not reaped it.
	_, rest, _ := strings.Cut(string(data), ") ")
	return strings.HasPrefix(rest, "Z")
}
