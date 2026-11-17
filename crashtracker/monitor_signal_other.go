// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func configureChild(cmd *exec.Cmd, pipe *os.File) (string, error) {
	cmd.ExtraFiles = []*os.File{pipe}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if foreground, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP); err == nil && foreground == syscall.Getpgrp() {
		cmd.SysProcAttr.Foreground = true
		cmd.SysProcAttr.Ctty = int(os.Stdin.Fd())
	}
	return "3", nil
}

func inheritedCrashPipe(fd uintptr) (_ *os.File, err error) {
	f := os.NewFile(fd, "crashtracker-pipe")
	if f == nil {
		return nil, os.ErrInvalid
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()
	flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	if flags&unix.O_ACCMODE == unix.O_RDONLY {
		return nil, errors.New("descriptor is not writable")
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errors.New("descriptor is not a pipe")
	}
	return f, nil
}

func supervisorSignals() (start func(*os.Process), stop func()) {
	ch := make(chan os.Signal, 256)
	var signals []os.Signal
	// Ignored panics outside the runtime signal table on Darwin.
	// os/signal accepts at most 64. The BSD runtime tables are smaller.
	maxSignal := syscall.Signal(64)
	switch runtime.GOOS {
	case "darwin":
		maxSignal = 31
	case "freebsd", "netbsd", "openbsd", "dragonfly":
		maxSignal = 32
	}
	for n := syscall.Signal(1); n <= maxSignal; n++ {
		switch n {
		case syscall.SIGKILL, syscall.SIGSTOP,
			syscall.SIGCHLD, syscall.SIGURG, syscall.SIGPROF, syscall.SIGPIPE,
			syscall.SIGTTIN, syscall.SIGTTOU,
			syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL,
			syscall.SIGTRAP, syscall.SIGSYS, syscall.SIGABRT:
			continue
		}
		if !signal.Ignored(n) {
			signals = append(signals, n)
		}
	}
	if len(signals) != 0 {
		signal.Notify(ch, signals...)
	}
	return func(child *os.Process) {
			go func() {
				for sig := range ch {
					// The child has its own group. Forward only to its PID so
					// terminal and group-directed signals do not arrive twice.
					_ = child.Signal(sig)
				}
			}()
		}, func() {
			signal.Stop(ch)
			close(ch)
		}
}

func exitWithChildStatus(state *os.ProcessState) {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		sig := status.Signal()
		// runtime/sigtab_linux_generic.go and runtime/signal_darwin.go give
		// only HUP, INT, and TERM _SigKill. Other Unix sigtabs use the same set.
		// ALRM has only _SigNotify. Re-raising other signals can dump this
		// supervisor or leave it alive. KILL bypasses the runtime entirely.
		switch sig {
		case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL:
			signal.Reset(sig)
			_ = syscall.Kill(os.Getpid(), sig)
			// Another OS thread can receive the signal after Kill returns.
			time.Sleep(100 * time.Millisecond)
		}
		os.Exit(128 + int(sig))
	}
	os.Exit(state.ExitCode())
}
