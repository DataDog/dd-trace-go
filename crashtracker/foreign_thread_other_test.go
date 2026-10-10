// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import (
	"strings"
	"syscall"
	"testing"
)

func TestBuildForeignThreadReport(t *testing.T) {
	r := buildForeignThreadReport(syscall.SIGSEGV)

	if r.Error.Type != "SIGSEGV" {
		t.Errorf("Error.Type = %q, want %q", r.Error.Type, "SIGSEGV")
	}
	if !r.Error.IsCrash {
		t.Error("Error.IsCrash = false, want true")
	}
	if r.Error.SourceType != "Crashtracking" {
		t.Errorf("Error.SourceType = %q, want %q", r.Error.SourceType, "Crashtracking")
	}
	if !strings.Contains(r.Error.Message, "not created by the Go runtime") {
		t.Errorf("Error.Message = %q, want it to explain the missing stack", r.Error.Message)
	}
	// There is genuinely nothing to report here: no stack, no thread list —
	// unlike every other Report this package builds, both must stay empty
	// rather than being populated with something misleading.
	if r.Error.Stack != nil {
		t.Errorf("Error.Stack = %+v, want nil", r.Error.Stack)
	}
	if len(r.Error.Threads) != 0 {
		t.Errorf("Error.Threads = %+v, want empty", r.Error.Threads)
	}
	if r.SigInfo == nil || r.SigInfo.SiSignoHuman != "SIGSEGV" {
		t.Errorf("SigInfo = %+v, want SiSignoHuman = SIGSEGV", r.SigInfo)
	}
	if r.OSInfo.Architecture == "" {
		t.Error("OSInfo.Architecture is empty")
	}
}

func TestBuildForeignThreadReportUnknownSignal(t *testing.T) {
	// foreignThreadSignalList only ever registers the four signals in
	// foreignThreadSignalNames, so this path is defensive, but it must not
	// panic or leave SigInfo half-built if it is ever reached.
	r := buildForeignThreadReport(syscall.SIGWINCH)

	if r.Error.Type != "UNKNOWN" {
		t.Errorf("Error.Type = %q, want %q", r.Error.Type, "UNKNOWN")
	}
}
