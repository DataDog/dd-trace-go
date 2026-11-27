// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"os"

	"golang.org/x/sys/unix"
)

// shouldTeeStderr reports whether the supervisor tees the application's
// stderr. A terminal user sees stderr directly, and the terminal modes of the
// application need the real terminal on its stderr.
func shouldTeeStderr(terminalMode, stderrIsTerminal bool) bool {
	return !terminalMode && !stderrIsTerminal
}

// openStderrSink returns a duplicate of the original stderr for the tee, or nil
// when the tee does not apply. The duplicate is not fd 2, so a broken pipe
// makes a write fail and does not raise SIGPIPE in the supervisor.
func openStderrSink(terminalMode bool) *os.File {
	if _, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_GETFD, 0); err != nil {
		return nil
	}
	// Only a terminal answers this request.
	_, err := unix.IoctlGetWinsize(unix.Stderr, unix.TIOCGWINSZ)
	if !shouldTeeStderr(terminalMode, err == nil) {
		return nil
	}
	fd, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_DUPFD_CLOEXEC, firstExtraFD)
	if err != nil {
		return nil
	}
	return os.NewFile(uintptr(fd), "crashtracker-stderr")
}
