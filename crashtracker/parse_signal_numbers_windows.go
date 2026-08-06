// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

// signalNumbers gives the POSIX values so a crash-dump fixture captured on
// Linux still parses deterministically in Windows CI. syscall.SIGBUS and
// friends are undefined on Windows, and a real Windows crash dump carries
// structured exceptions rather than SIG* lines, so this table only ever
// serves fixture parsing, never a live crash on this platform.
var signalNumbers = map[string]int{
	"SIGQUIT": 3,
	"SIGILL":  4,
	"SIGTRAP": 5,
	"SIGABRT": 6,
	"SIGBUS":  7,
	"SIGFPE":  8,
	"SIGSEGV": 11,
}
