// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	case "supervisor":
		_, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildForegroundHelper$")
		cmd.Env = append(os.Environ(), foregroundRoleEnv+"=application")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		// Detached mode, as with docker run -t, where no job-control shell runs.
		if _, err := configureChild(cmd, w, false); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		closeInheritedDescriptors(cmd, w)
		_ = cmd.Wait()
	case "supervisor-exec-fails":
		// The child's exec fails after Go moved the terminal foreground to it.
		// The supervisor then returns to the caller, which reads the terminal.
		notExecutable := filepath.Join(dir, "not-executable")
		if err := os.WriteFile(notExecutable, []byte("not a program"), 0600); err != nil {
			t.Fatal(err)
		}
		_, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
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
	}
}

// recordTerminalLine reads one line from stdin and stores it in dir.
func recordTerminalLine(t *testing.T, dir string) {
	t.Helper()
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

// runOnTerminal runs the helper in the given role as a session leader on a pty
// and expects it, or its child, to read a line that the test types.
func runOnTerminal(t *testing.T, role, timeoutMessage string) {
	t.Helper()
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
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
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedChildForegroundHelper$")
	cmd.Env = append(os.Environ(), foregroundRoleEnv+"="+role, "_CRASHTRACKER_FOREGROUND_DIR="+dir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	go func() { _, _ = io.Copy(io.Discard, master) }()
	// Give the application time to start its read before the input arrives.
	time.Sleep(500 * time.Millisecond)
	if _, err := master.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "line")); err == nil {
			if string(data) != "hello\n" {
				t.Fatalf("application read %q, want %q", data, "hello\n")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(timeoutMessage)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
