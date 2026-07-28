// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import "runtime"

// signalNumbers maps the signal names a Go crash dump reports to a POSIX
// signal number. The dump is arbitrary text — captured on any platform,
// parsed on any platform (a fixture captured on Linux is parsed the same way
// in Windows CI) — so this is not conditioned on the current GOOS beyond the
// one signal whose number actually differs across the platforms dd-trace-go
// targets: SIGBUS is 7 on Linux, 10 on Darwin. Every other signal here has the
// same number on both. Windows has no native equivalent to any of these, but
// falls through to the Linux/default value like any other non-Darwin GOOS,
// since these are hardcoded POSIX constants rather than syscall lookups
// (syscall.SIGBUS and friends are undefined on Windows).
var signalNumbers = map[string]int{
	"SIGILL":  4,
	"SIGTRAP": 5,
	"SIGABRT": 6,
	"SIGBUS":  sigbusNumber(),
	"SIGFPE":  8,
	"SIGSEGV": 11,
}

func sigbusNumber() int {
	if runtime.GOOS == "darwin" {
		return 10
	}
	return 7
}
