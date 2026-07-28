// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import "syscall"

// signalNumbers maps the signal names a Go crash dump reports to a POSIX
// signal number, using the real syscall constants for the platform this
// binary is built for. This is correct for every Unix GOOS/GOARCH pair Go
// supports, including the ones where SIGBUS diverges from its usual value
// (7 on most Linux targets, but 10 on Darwin/BSD and on linux/mips*) — there
// is no simpler rule that covers this than the constants the runtime itself
// uses.
var signalNumbers = map[string]int{
	"SIGILL":  int(syscall.SIGILL),
	"SIGTRAP": int(syscall.SIGTRAP),
	"SIGABRT": int(syscall.SIGABRT),
	"SIGBUS":  int(syscall.SIGBUS),
	"SIGFPE":  int(syscall.SIGFPE),
	"SIGSEGV": int(syscall.SIGSEGV),
}
