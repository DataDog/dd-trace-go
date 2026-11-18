// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"bytes"
	"fmt"
	"io"
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

func TestSupervisorTerminalHelper(t *testing.T) {
	role := os.Getenv("_CRASHTRACKER_TERMINAL_ROLE")
	if role == "" {
		return
	}
	dir := os.Getenv("_CRASHTRACKER_REGRESSION_DIR")
	if role == "shell" {
		runTerminalShell(t, dir)
		return
	}
	if err := crashtracker.Start(); err != nil {
		t.Fatal(err)
	}
	interrupts := make(chan os.Signal, 8)
	signal.Notify(interrupts, syscall.SIGINT)
	writeReady(t, dir)
	<-interrupts
	count := 1
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-interrupts:
			count++
		case <-timer.C:
			if err := os.WriteFile(filepath.Join(dir, "interrupts"), []byte(strconv.Itoa(count)), 0600); err != nil {
				t.Fatal(err)
			}
			signal.Stop(interrupts)
			signal.Reset(syscall.SIGINT)
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			time.Sleep(100 * time.Millisecond)
			os.Exit(130)
		}
	}
}

func runTerminalShell(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorTerminalHelper$")
	cmd.Env = append(os.Environ(), "_CRASHTRACKER_TERMINAL_ROLE=job")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// A shell keeps the job's group non-orphaned so terminal stop signals
	// have their normal effect. A Setsid supervisor alone cannot model this.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Release()
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if data, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}()
	signal.Ignore(syscall.SIGTTOU)
	if err := os.WriteFile(filepath.Join(dir, "supervisor"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	status := waitTerminalStatus(t, cmd.Process.Pid, unix.WUNTRACED)
	if !status.Stopped() {
		if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("not-stopped"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Fatal("supervisor did not stop with its job")
	}
	if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("stopped"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, filepath.Join(dir, "continue"))
	if err := unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	status = waitTerminalStatus(t, cmd.Process.Pid, unix.WCONTINUED)
	if !status.Continued() {
		t.Fatalf("supervisor did not continue: %v", status)
	}
	if err := os.WriteFile(filepath.Join(dir, "continued"), []byte("continued"), 0600); err != nil {
		t.Fatal(err)
	}
	status = waitTerminalStatus(t, cmd.Process.Pid, 0)
	if !status.Signaled() || status.Signal() != unix.SIGINT {
		t.Fatalf("supervisor exit status = %v, want SIGINT", status)
	}
}

func waitTerminalStatus(t *testing.T, pid, options int) unix.WaitStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status unix.WaitStatus
		got, err := unix.Wait4(pid, &status, options|unix.WNOHANG, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got == pid {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	return 0
}

func terminalProcessState(t *testing.T, pid int) byte {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(data), ") ")
	if !ok || len(rest) == 0 {
		t.Fatalf("invalid process state: %s", data)
	}
	return rest[0]
}

func TestSupervisorTerminalJobControl(t *testing.T) {
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(masterFD), "pty-master")
	defer master.Close()
	if err := unix.IoctlSetPointerInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slaveFD, err := unix.Open(fmt.Sprintf("/dev/pts/%d", number), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	slave := os.NewFile(uintptr(slaveFD), "pty-slave")
	defer slave.Close()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorTerminalHelper$")
	cmd.Env = append(filterE2EEnv(os.Environ()), "_CRASHTRACKER_TERMINAL_ROLE=shell",
		"_CRASHTRACKER_REGRESSION_DIR="+dir, "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	var output bytes.Buffer
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, master)
		close(drained)
	}()
	defer func() {
		_ = master.Close()
		<-drained
	}()
	supervisor, err := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "supervisor"))))
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := master.Write([]byte{'\x1a'}); err != nil {
		t.Fatal(err)
	}
	if got := string(waitForFile(t, filepath.Join(dir, "stopped"))); got != "stopped" {
		t.Fatalf("wait4(WUNTRACED): %s", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for terminalProcessState(t, supervisor) != 'T' || terminalProcessState(t, child) != 'T' {
		if time.Now().After(deadline) {
			t.Fatal("supervisor and app did not both stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "continue"), []byte("fg"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, filepath.Join(dir, "continued"))
	deadline = time.Now().Add(time.Second)
	for terminalProcessState(t, supervisor) == 'T' || terminalProcessState(t, child) == 'T' {
		if time.Now().After(deadline) {
			t.Fatal("supervisor and app did not both continue")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Run("SIGINTToSupervisorPIDOnly", func(t *testing.T) {
		// In terminal mode the terminal delivers SIGINT to the job group, so
		// the supervisor must not forward one that only it received.
		if err := syscall.Kill(supervisor, syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "interrupts")); err == nil {
				t.Fatal("the application received a SIGINT sent only to the supervisor PID")
			}
			if state := terminalProcessState(t, supervisor); state == 'Z' || state == 'X' {
				t.Fatal("the supervisor exited on a SIGINT sent to its PID")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if terminalProcessState(t, child) == 'Z' {
			t.Fatal("the application exited after a SIGINT sent to the supervisor PID")
		}
	})
	if _, err := master.Write([]byte{'\x03'}); err != nil {
		t.Fatal(err)
	}
	if err := waitForVictim(t, cmd, 10*time.Second); err != nil {
		_ = master.Close()
		<-drained
		t.Fatalf("terminal shell: %v: %s", err, output.Bytes())
	}
	if got := string(waitForFile(t, filepath.Join(dir, "interrupts"))); got != "1" {
		t.Fatalf("app observed %s SIGINT signals, want 1", got)
	}
}
