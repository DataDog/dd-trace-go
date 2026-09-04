// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// foreignThreadSignalNames maps the fixed set of synchronous fault signals a
// thread created entirely by native code can raise to their names, in the
// order registered with signal.Notify. This is the inverse of the numeric
// lookup signalNumbers provides for crash-dump parsing: parsing starts from
// dump text and needs a number, this starts from an os.Signal value (which
// on POSIX is a syscall.Signal) and needs a name.
var foreignThreadSignalNames = map[syscall.Signal]string{
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGILL:  "SIGILL",
	syscall.SIGFPE:  "SIGFPE",
}

func foreignThreadSignalList() []os.Signal {
	sigs := make([]os.Signal, 0, len(foreignThreadSignalNames))
	for s := range foreignThreadSignalNames {
		sigs = append(sigs, s)
	}
	return sigs
}

// startForeignThreadSignals registers a best-effort reporter for a fault on
// a thread runtime/debug.SetCrashOutput cannot observe at all: one created
// entirely by native code, which never entered Go (see
// WithForeignThreadSignals). Runs in its own goroutine and returns
// immediately; does not block Start.
func startForeignThreadSignals(cfg *config) {
	sigs := foreignThreadSignalList()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	go func() {
		sig := <-ch
		// Report before resetting, not after: measured the window between a
		// reset and the process dying from the fault's near-certain next
		// occurrence at under 200 microseconds — far too short for any
		// upload to complete, which would make delivery not just
		// best-effort but effectively never. Reporting first instead means
		// the upload runs while the underlying fault keeps retrying in the
		// background (confirmed separately: that retrying, left
		// unaddressed, loops indefinitely across multiple CPU cores) —
		// bounded CPU cost for the length of one upload attempt, in
		// exchange for an upload that can actually complete.
		report := buildForeignThreadReport(sig)
		report.DDTags = buildDDTags(cfg, report)
		if err := uploadReport(cfg, report); err != nil {
			log.Warn("crashtracker: foreign-thread signal upload failed: %v", err.Error())
		}

		// Only now reset to default disposition, so the near-certain next
		// occurrence of the fault terminates the process normally instead
		// of continuing to loop.
		signal.Reset(sigs...)
	}()
}

// buildForeignThreadReport builds a minimal, best-effort Report for a signal
// observed on a thread the Go runtime never tracked. There is no stack or
// register context available for that thread — nothing in this process
// records one — so unlike every other Report this package builds, Stack and
// Threads are intentionally left empty rather than populated from a parsed
// dump.
func buildForeignThreadReport(sig os.Signal) *Report {
	name := "UNKNOWN"
	if s, ok := sig.(syscall.Signal); ok {
		if n, ok := foreignThreadSignalNames[s]; ok {
			name = n
		}
	}
	return &Report{
		Timestamp: time.Now().UnixMilli(),
		DDSource:  ddSource,
		Error: Error{
			Type:       name,
			Message:    "signal " + name + " observed on a thread not created by the Go runtime; no stack trace is available for this thread",
			IsCrash:    true,
			SourceType: "Crashtracking",
		},
		OSInfo: osInfo(),
		SigInfo: &SigInfo{
			SiSignoHuman: name,
			SiSigno:      signalNumbers[name],
		},
	}
}
