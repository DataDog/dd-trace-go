// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

func TestSupervisorTeardownHelper(t *testing.T) {
	if os.Getenv("_CRASHTRACKER_TEARDOWN") != "1" {
		return
	}
	if err := crashtracker.Start(); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("_CRASHTRACKER_TEARDOWN_DIR")
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "crash")); err == nil {
			panic("teardown invariant crash")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSupervisorTeardownInvariant(t *testing.T) {
	t.Parallel()
	var received atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A slow intake makes an upload that outlives the top process observable.
		time.Sleep(250 * time.Millisecond)
		body := decompressGzipBody(t, r)
		assertCrashReport(t, body, "panic", "teardown invariant crash")
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorTeardownHelper$")
	cmd.Env = append(filterE2EEnv(os.Environ()),
		"_CRASHTRACKER_TEARDOWN=1", "_CRASHTRACKER_TEARDOWN_DIR="+dir,
		"DD_CRASHTRACKING_ENABLED=true", "DD_TRACE_AGENT_URL="+srv.URL)
	// A real file prevents exec.Cmd.Wait from waiting for a surviving monitor
	// to close an output-copy pipe after the top process has already exited.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	cmd.Stdout, cmd.Stderr = null, null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(dir, "ready"))
	// Each topology gives its child a separate process group. Record every
	// descendant before the crash so teardown does not depend on reparenting.
	descendants := processDescendants(t, cmd.Process.Pid)
	if len(descendants) == 0 {
		t.Fatal("helper has no descendant")
	}
	teardown := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		for _, pid := range descendants {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	t.Cleanup(teardown)
	if err := os.WriteFile(filepath.Join(dir, "crash"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_ = waitForVictim(t, cmd, 15*time.Second)
	alreadyReceived := received.Load()
	teardown()
	if !alreadyReceived {
		t.Fatal("report had not arrived before the top process exited")
	}
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) != 0 {
			return data
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", path)
	return nil
}

func processDescendants(t *testing.T, root int) []int {
	t.Helper()
	output, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		t.Fatal(err)
	}
	children := make(map[int][]int)
	for line := range strings.SplitSeq(string(output), "\n") {
		var pid, ppid int
		if _, err := fmt.Sscan(line, &pid, &ppid); err == nil {
			children[ppid] = append(children[ppid], pid)
		}
	}
	queue := append([]int(nil), children[root]...)
	for i := 0; i < len(queue); i++ {
		queue = append(queue, children[queue[i]]...)
	}
	return queue
}
