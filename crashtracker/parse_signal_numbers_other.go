// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import "syscall"

// signalNumbers maps the signal names the Go runtime reports on a crash to
// their platform's POSIX signal numbers. Values differ across platforms for
// some signals (SIGBUS is 7 on Linux, 10 on Darwin), so these come from
// syscall's platform-specific constants rather than a single hardcoded table.
// Only the signals that surface as fatal crashes are included; unknown
// signals fall back to 0.
var signalNumbers = map[string]int{
	"SIGILL":  int(syscall.SIGILL),
	"SIGTRAP": int(syscall.SIGTRAP),
	"SIGABRT": int(syscall.SIGABRT),
	"SIGBUS":  int(syscall.SIGBUS),
	"SIGFPE":  int(syscall.SIGFPE),
	"SIGSEGV": int(syscall.SIGSEGV),
}
