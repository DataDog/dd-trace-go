// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !windows

package crashtracker

import "testing"

func TestShouldTeeStderr(t *testing.T) {
	for _, tt := range []struct {
		terminalMode     bool
		stderrIsTerminal bool
		want             bool
	}{
		{terminalMode: false, stderrIsTerminal: false, want: true},
		{terminalMode: true, stderrIsTerminal: false, want: false},
		{terminalMode: false, stderrIsTerminal: true, want: false},
		{terminalMode: true, stderrIsTerminal: true, want: false},
	} {
		if got := shouldTeeStderr(tt.terminalMode, tt.stderrIsTerminal); got != tt.want {
			t.Errorf("shouldTeeStderr(terminalMode=%v, stderrIsTerminal=%v) = %v, want %v", tt.terminalMode, tt.stderrIsTerminal, got, tt.want)
		}
	}
}
