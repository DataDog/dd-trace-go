// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func supervisorTerminalMode() bool {
	fd, err := unix.Open("/dev/tty", unix.O_RDONLY|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	hasTTY := err == nil
	if hasTTY {
		_ = unix.Close(fd)
	}
	return terminalMode(hasTTY, os.Getpid(), os.Getppid())
}

func terminalMode(hasTTY bool, pid, ppid int) bool {
	return hasTTY && pid != 1 && ppid > 1
}

func configureChild(cmd *exec.Cmd, pipe *os.File, terminalMode bool) (_ string, err error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: !terminalMode}
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return "", err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			for _, f := range cmd.ExtraFiles {
				if f != nil {
					_ = f.Close()
				}
			}
		}
	}()
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil || fd < 3 {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		// Darwin's /dev/fd also lists closed slots. A concurrent close can
		// leave a stale entry on other platforms too.
		if errors.Is(err, unix.EBADF) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect inherited fd %d: %w", fd, err)
		}
		if flags&unix.FD_CLOEXEC != 0 {
			continue
		}
		// Own a duplicate so a failed re-exec cannot close the app's fd.
		dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
		if err != nil {
			return "", fmt.Errorf("duplicate inherited fd %d: %w", fd, err)
		}
		for len(cmd.ExtraFiles) <= fd-3 {
			cmd.ExtraFiles = append(cmd.ExtraFiles, nil)
		}
		cmd.ExtraFiles[fd-3] = os.NewFile(uintptr(dup), "inherited-fd-"+name)
	}
	marker := strconv.Itoa(3 + len(cmd.ExtraFiles))
	cmd.ExtraFiles = append(cmd.ExtraFiles, pipe)
	return marker, nil
}

func closeInheritedDescriptors(cmd *exec.Cmd, pipe *os.File) {
	for i, f := range cmd.ExtraFiles {
		if f != nil && f != pipe {
			_ = unix.Close(3 + i)
		}
	}
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

func supervisorSignals(terminalMode bool) (start func(*os.Process), stop func()) {
	ch := make(chan os.Signal, 256)
	var signals, terminalSignals []os.Signal
	var terminalSink chan os.Signal
	// Ignored panics outside the runtime signal table on Darwin.
	// os/signal accepts at most 64. The BSD runtime tables are smaller.
	maxSignal := syscall.Signal(64)
	var sigInfo syscall.Signal
	switch runtime.GOOS {
	case "darwin":
		maxSignal = 31
		sigInfo = 29 // SIGINFO on Darwin and BSDs.
	case "freebsd", "netbsd", "openbsd", "dragonfly":
		maxSignal = 32
		sigInfo = 29
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
		if terminalMode {
			// The terminal delivers these signals to the shared job group.
			if n == sigInfo {
				continue
			}
			switch n {
			case syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP:
				if !signal.Ignored(n) {
					terminalSignals = append(terminalSignals, n)
				}
				continue
			case syscall.SIGWINCH, syscall.SIGTSTP, syscall.SIGCONT:
				continue
			}
		}
		if !signal.Ignored(n) {
			signals = append(signals, n)
		}
	}
	if len(signals) != 0 {
		signal.Notify(ch, signals...)
	}
	if len(terminalSignals) != 0 {
		// runtime.sigignore installs SIG_IGN, which sigdisable does not
		// undo. A non-forwarded sink lets Stop restore fatal INT/HUP.
		// Catching before exec is safe: exec resets caught signals.
		terminalSink = make(chan os.Signal, len(terminalSignals))
		signal.Notify(terminalSink, terminalSignals...)
	}
	return func(child *os.Process) {
			// Apply ignores only after exec so the app keeps its own defaults.
			signal.Ignore(syscall.SIGPIPE)
			go func() {
				for sig := range ch {
					_ = child.Signal(sig)
				}
			}()
		}, func() {
			signal.Stop(ch)
			close(ch)
			if terminalSink != nil {
				signal.Stop(terminalSink)
			}
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
