// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package goversion

import (
	"runtime"
	"testing"
)

func TestVersionIsGoTip(t *testing.T) {
	cases := []struct {
		name string
		v    string
		want bool
	}{
		{"release patch", "go1.24.1", false},
		{"release minor", "go1.25", false},
		{"devel long", "devel go1.25-abc1234 Tue Jul 22 15:00:00 2026 +0000", true},
		{"devel short", "devel +abc1234", true},
		{"empty", "", false},
		{"unknown", "weird", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionIsGoTip(tt.v); got != tt.want {
				t.Errorf("versionIsGoTip(%q) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestSkipIfGoTipReleasedToolchainIsNoOp(t *testing.T) {
	if versionIsGoTip(runtime.Version()) {
		t.Skipf("running under unreleased toolchain %s; skip-path covered by the gotip job", runtime.Version())
	}
	// On a released toolchain SkipIfGoTip must be a no-op: reaching the line
	// after the call proves it neither skipped nor panicked.
	SkipIfGoTip(t, "must be ignored on released toolchain: %d", 1)
}
