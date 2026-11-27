// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker_test

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

func setHelperParentDeathSignal(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	// Linux delivers the signal when the thread that starts the helper exits.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

func TestSupervisorDeathKillsChild(t *testing.T) {
	cmd, dir := startSupervisorHelper(t, "wait-for-signal")
	pid, err := strconv.Atoi(string(waitForFile(t, filepath.Join(dir, "ready"))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
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
	if err := startVictim(t, cmd); err != nil {
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

func TestSupervisorStartFailsOpenWithoutDevFD(t *testing.T) {
	const noDevFDEnv = "_CRASHTRACKER_NO_DEV_FD"
	const mountUnavailable = 77
	if os.Getenv(noDevFDEnv) != "" {
		if err := hideDevFD(); err != nil {
			if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(mountUnavailable)
			}
			t.Fatal(err)
		}
		if err := crashtracker.Start(); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "/dev/fd") {
			t.Fatalf("Start without /dev/fd = %v, want an error for /dev/fd", err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorStartFailsOpenWithoutDevFD$")
	cmd.Env = append(filterE2EEnv(os.Environ()), noDevFDEnv+"=1", "DD_CRASHTRACKING_ENABLED=true", "GORACE=atexit_sleep_ms=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:                     true,
		Cloneflags:                 unix.CLONE_NEWUSER | unix.CLONE_NEWNS,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := startVictim(t, cmd); errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSPC) {
		t.Skipf("cannot create a user and mount namespace without root: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	if err := waitForVictim(t, cmd, 5*time.Second); err != nil {
		if cmd.ProcessState.ExitCode() == mountUnavailable {
			t.Skipf("cannot mount /dev without root: %s", &out)
		}
		t.Fatalf("fail-open helper: %v: %s", err, &out)
	}
}

func hideDevFD() error {
	// Make the mounts private before the helper hides /dev/fd.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}
	if err := unix.Mount("tmpfs", "/dev", "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("mount private /dev: %w", err)
	}
	return nil
}
