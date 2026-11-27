// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const foregroundRoleEnv = "_CRASHTRACKER_FOREGROUND_ROLE"

// TestDetachedChildForegroundHelper runs as the supervisor or as the
// application in TestDetachedChildReadsTerminal.
func TestDetachedChildForegroundHelper(t *testing.T) {
	dir := os.Getenv("_CRASHTRACKER_FOREGROUND_DIR")
	switch os.Getenv(foregroundRoleEnv) {
	case "background-shell":
		cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildForegroundHelper$")
		cmd.Env = append(os.Environ(), foregroundRoleEnv+"=background-supervisor")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := StartTestProcess(t, cmd); err != nil {
			t.Fatal(err)
		}
		if err := WaitTestProcess(t, cmd, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	case "supervisor", "background-supervisor":
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildForegroundHelper$")
		defer r.Close()
		defer w.Close()
		role := "application"
		if os.Getenv(foregroundRoleEnv) == "background-supervisor" {
			role = "background-application"
		}
		cmd.Env = append(os.Environ(), foregroundRoleEnv+"="+role)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		// Detached mode, as with docker run -t, where no job-control shell runs.
		if _, err := configureChild(cmd, w, false); err != nil {
			t.Fatal(err)
		}
		if err := StartTestProcess(t, cmd); err != nil {
			t.Fatal(err)
		}
		closeInheritedDescriptors(cmd, w)
		if err := WaitTestProcess(t, cmd, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	case "supervisor-exec-fails":
		// The child's exec fails after Go moved the terminal foreground to it.
		// The supervisor then returns to the caller, which reads the terminal.
		notExecutable := filepath.Join(dir, "not-executable")
		if err := os.WriteFile(notExecutable, []byte("not a program"), 0600); err != nil {
			t.Fatal(err)
		}
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		cmd := exec.Command(notExecutable)
		if _, err := configureChild(cmd, w, false); err != nil {
			t.Fatal(err)
		}
		if err := startChild(cmd); err == nil {
			t.Fatal("start of a non-executable file succeeded")
		}
		recordTerminalLine(t, dir)
	case "application":
		recordTerminalLine(t, dir)
	case "background-application":
		foreground, err := unix.IoctlGetInt(0, unix.TIOCGPGRP)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "foreground"), []byte(strconv.Itoa(foreground)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// recordTerminalLine reads one line from stdin and stores it in dir.
func recordTerminalLine(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "line"), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
}

// In detached mode the application runs in its own process group. When the
// supervisor owns the terminal foreground, the application must get it, or
// its first terminal read stops it with SIGTTIN.
func TestDetachedChildReadsTerminal(t *testing.T) {
	runOnTerminal(t, "supervisor", "the application did not read from the terminal: it does not own the foreground")
}

// If the exec of the application fails, the supervisor must get the terminal
// foreground back, or its next terminal read stops it with SIGTTIN.
func TestDetachedChildExecFailureRestoresForeground(t *testing.T) {
	runOnTerminal(t, "supervisor-exec-fails", "the supervisor did not read from the terminal: the failed start left the foreground with the dead child group")
}

func TestDetachedChildDoesNotTakeForegroundFromShell(t *testing.T) {
	cmd, _, dir, output := startForegroundHelper(t, "background-shell")
	if err := WaitTestProcess(t, cmd, 10*time.Second); err != nil {
		t.Fatalf("background supervisor: %v: %s", err, output())
	}
	data, err := os.ReadFile(filepath.Join(dir, "foreground"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strconv.Itoa(cmd.Process.Pid); string(data) != want {
		t.Fatalf("terminal foreground group = %s, want the shell group %s", data, want)
	}
}

func runOnTerminal(t *testing.T, role, failure string) {
	t.Helper()
	cmd, master, dir, output := startForegroundHelper(t, role)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal helper did not become ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := master.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if err := WaitTestProcess(t, cmd, 10*time.Second); err != nil {
		t.Fatalf("%s: %v: %s", failure, err, output())
	}
	data, err := os.ReadFile(filepath.Join(dir, "line"))
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("terminal read = %q, %v, want hello", data, err)
	}
}

func startForegroundHelper(t *testing.T, role string) (*exec.Cmd, *os.File, string, func() string) {
	t.Helper()
	master, slave := OpenTestPTY(t)
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildForegroundHelper$")
	cmd.Env = append(os.Environ(), foregroundRoleEnv+"="+role, "_CRASHTRACKER_FOREGROUND_DIR="+dir, "GORACE=atexit_sleep_ms=0")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := StartTestProcess(t, cmd); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var output bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&output, master)
	}()
	return cmd, master, dir, func() string {
		_ = master.Close()
		<-drained
		return output.String()
	}
}

// OpenTestPTY opens a PTY or skips when the host does not provide one.
func OpenTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Skipf("cannot open PTY master: %v", err)
	}
	master := os.NewFile(uintptr(masterFD), "pty-master")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open PTY slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}
