// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseCrashDump(t *testing.T) {
	tests := []struct {
		name              string
		fixture           string
		wantType          string
		wantMessageSubstr string
		wantThreads       int
		wantMinThreads    int
		wantTopFunction   string
		wantTopFileSuffix string
		wantSignal        bool
		wantSigName       string
		wantSigNo         int
	}{
		{
			name:        "panic",
			fixture:     "panic_simple.txt",
			wantType:    "panic",
			wantThreads: 3,
		},
		{
			name:        "concurrent map write",
			fixture:     "concurrent_map_write.txt",
			wantType:    "runtime.plainError",
			wantThreads: 2,
		},
		{
			name:        "sigsegv",
			fixture:     "sigsegv.txt",
			wantType:    "UnixSignal",
			wantThreads: 3,
			wantSignal:  true,
			wantSigName: "SIGSEGV",
			wantSigNo:   11,
		},
		{
			name:              "deadlock",
			fixture:           "deadlock.txt",
			wantType:          "runtime.plainError",
			wantMessageSubstr: "all goroutines are asleep - deadlock!",
			wantThreads:       1,
			wantTopFunction:   "main.main",
			wantTopFileSuffix: "deadlock.go",
		},
		{
			name:              "stack exhaustion",
			fixture:           "stack_exhaustion.txt",
			wantType:          "runtime.plainError",
			wantMessageSubstr: "stack overflow",
			wantMinThreads:    1,
			wantTopFunction:   "main.recurse",
			wantTopFileSuffix: "stack_exhaustion.go",
		},
		{
			name:              "panic traceback all",
			fixture:           "panic_traceback_all.txt",
			wantType:          "panic",
			wantMessageSubstr: "traceback all fixture",
			wantMinThreads:    2,
			wantTopFunction:   "main.main",
			wantTopFileSuffix: "panic_traceback_all.go",
		},
		{
			name:              "close closed channel",
			fixture:           "close_closed_channel.txt",
			wantType:          "panic",
			wantMessageSubstr: "close of closed channel",
			wantThreads:       1,
			wantTopFunction:   "main.main",
			wantTopFileSuffix: "close_closed_channel.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dump := readFixture(t, tt.fixture)
			r := parseCrashDump(dump)

			if r == nil {
				t.Fatal("parseCrashDump returned nil")
			}

			if r.DDSource != "crashtracker" {
				t.Errorf("DDSource = %q, want %q", r.DDSource, "crashtracker")
			}
			if r.Timestamp <= 0 {
				t.Errorf("Timestamp = %d, want > 0", r.Timestamp)
			}
			if !r.Error.IsCrash {
				t.Error("Error.IsCrash = false, want true")
			}

			if r.Error.Type != tt.wantType {
				t.Errorf("Error.Type = %q, want %q", r.Error.Type, tt.wantType)
			}
			if r.Error.Message == "" {
				t.Error("Error.Message is empty, want non-empty")
			}
			if tt.wantMessageSubstr != "" && !strings.Contains(r.Error.Message, tt.wantMessageSubstr) {
				t.Errorf("Error.Message = %q, want substring %q", r.Error.Message, tt.wantMessageSubstr)
			}

			if r.Error.Stack == nil {
				t.Fatal("Error.Stack is nil, want non-nil")
			}
			if len(r.Error.Stack.Frames) == 0 {
				t.Error("Error.Stack.Frames is empty, want frames")
			}
			if r.Error.Stack.Incomplete {
				t.Error("Error.Stack.Incomplete = true, want false")
			}
			if r.Error.Stack.Format != "Datadog Crashtracker 1.0" {
				t.Errorf("Error.Stack.Format = %q, want %q", r.Error.Stack.Format, "Datadog Crashtracker 1.0")
			}
			if tt.wantTopFunction != "" && len(r.Error.Stack.Frames) > 0 {
				top := r.Error.Stack.Frames[0]
				if top.Function != tt.wantTopFunction {
					t.Errorf("top frame Function = %q, want %q", top.Function, tt.wantTopFunction)
				}
				if !strings.HasSuffix(top.File, tt.wantTopFileSuffix) {
					t.Errorf("top frame File = %q, want suffix %q", top.File, tt.wantTopFileSuffix)
				}
				if top.Line <= 0 {
					t.Errorf("top frame Line = %d, want > 0", top.Line)
				}
			}

			if tt.wantThreads > 0 {
				if got := len(r.Error.Threads); got != tt.wantThreads {
					t.Errorf("len(Error.Threads) = %d, want %d", got, tt.wantThreads)
				}
			}
			if tt.wantMinThreads > 0 {
				if got := len(r.Error.Threads); got < tt.wantMinThreads {
					t.Errorf("len(Error.Threads) = %d, want >= %d", got, tt.wantMinThreads)
				}
			}

			crashedCount := 0
			for _, th := range r.Error.Threads {
				if th.Crashed {
					crashedCount++
				}
			}
			if crashedCount != 1 {
				t.Errorf("crashed thread count = %d, want exactly 1", crashedCount)
			}

			// Every parsed frame must carry a function name and a source
			// location, and the crashing thread's name must be reported.
			if r.Error.ThreadName == "" {
				t.Error("Error.ThreadName is empty, want the crashing goroutine name")
			}
			for i, f := range r.Error.Stack.Frames {
				if f.Function == "" {
					t.Errorf("frame %d has empty Function", i)
				}
				if f.File == "" {
					t.Errorf("frame %d (%s) has empty File", i, f.Function)
				}
				if f.Line <= 0 {
					t.Errorf("frame %d (%s) has non-positive Line %d", i, f.Function, f.Line)
				}
			}

			if r.OSInfo.Architecture != runtime.GOARCH {
				t.Errorf("OSInfo.Architecture = %q, want %q", r.OSInfo.Architecture, runtime.GOARCH)
			}
			if r.OSInfo.Bitness != "64-bit" {
				t.Errorf("OSInfo.Bitness = %q, want %q", r.OSInfo.Bitness, "64-bit")
			}

			if tt.wantSignal {
				if r.SigInfo == nil {
					t.Fatal("SigInfo is nil, want non-nil for a signal crash")
				}
				if r.SigInfo.SiSignoHuman != tt.wantSigName {
					t.Errorf("SigInfo.SiSignoHuman = %q, want %q", r.SigInfo.SiSignoHuman, tt.wantSigName)
				}
				if r.SigInfo.SiSigno != tt.wantSigNo {
					t.Errorf("SigInfo.SiSigno = %d, want %d", r.SigInfo.SiSigno, tt.wantSigNo)
				}
			} else if r.SigInfo != nil {
				t.Errorf("SigInfo = %+v, want nil for a non-signal crash", r.SigInfo)
			}
		})
	}
}

func TestParseCrashDumpCrashingGoroutineFrames(t *testing.T) {
	// The panic fixture's crashing goroutine is goroutine 1 with four frames:
	// panic, main.inner, main.middle, main.main.
	dump := readFixture(t, "panic_simple.txt")
	r := parseCrashDump(dump)

	if r.Error.ThreadName != "goroutine 1" {
		t.Errorf("ThreadName = %q, want %q", r.Error.ThreadName, "goroutine 1")
	}
	if got := len(r.Error.Stack.Frames); got != 4 {
		t.Fatalf("crashing goroutine frame count = %d, want 4", got)
	}
	wantFns := []string{"panic", "main.inner", "main.middle", "main.main"}
	for i, want := range wantFns {
		if got := r.Error.Stack.Frames[i].Function; got != want {
			t.Errorf("frame %d Function = %q, want %q", i, got, want)
		}
	}
	// main.main() is the last frame; its location is the real source line.
	last := r.Error.Stack.Frames[3]
	if last.File == "" || last.Line == 0 {
		t.Errorf("main.main frame missing location: %+v", last)
	}
}

func TestParseCrashDumpSignalDetails(t *testing.T) {
	dump := readFixture(t, "sigsegv.txt")
	r := parseCrashDump(dump)

	if r.SigInfo == nil {
		t.Fatal("SigInfo is nil")
	}
	if r.SigInfo.SiCode != 2 {
		t.Errorf("SigInfo.SiCode = %d, want 2", r.SigInfo.SiCode)
	}
	if r.SigInfo.SiAddr != "0x0" {
		t.Errorf("SigInfo.SiAddr = %q, want %q", r.SigInfo.SiAddr, "0x0")
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return b
}
