// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitDumpHandlesOversizedLine(t *testing.T) {
	// A single line longer than the scanner's token limit (maxCrashDumpSize)
	// trips bufio.Scanner's ErrTooLong. This is the only producer of a thread
	// with Incomplete: true and zero frames, and it feeds reportIncomplete, so
	// it decides whether a torn dump is reported as partial or silently as
	// complete — worth covering even though a dump this size is rare. A
	// normal line precedes the oversized one so the test also proves lines
	// scanned before the failure point survive into preamble.
	dump := append([]byte("panic: boom\n"), bytes.Repeat([]byte("x"), maxCrashDumpSize+1)...)

	preamble, threads := splitDump(dump)

	if len(threads) != 1 {
		t.Fatalf("len(threads) = %d, want 1", len(threads))
	}
	if !threads[0].Crashed {
		t.Error("synthesized thread is not marked Crashed")
	}
	if !threads[0].Stack.Incomplete {
		t.Error("synthesized thread's Stack.Incomplete = false, want true")
	}
	if len(threads[0].Stack.Frames) != 0 {
		t.Errorf("synthesized thread has %d frames, want 0", len(threads[0].Stack.Frames))
	}
	if len(preamble) != 1 || preamble[0] != "panic: boom" {
		t.Errorf("preamble = %v, want the one line scanned before the failure point", preamble)
	}
}

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
			wantType:    "SIGSEGV",
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
			if r.OSInfo.Version == "" {
				t.Error("OSInfo.Version is empty")
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

func TestParseSignalQuit(t *testing.T) {
	// SetCrashOutput captures SIGQUIT too (an operator Ctrl-\ or a diagnostic
	// dump-all-goroutines signal), and the runtime reports it in the
	// top-level "SIG…: …" form, e.g. "SIGQUIT: quit".
	preamble := []string{"SIGQUIT: quit"}
	got := parseSignal(preamble)
	if got == nil {
		t.Fatal("parseSignal returned nil, want a populated SigInfo")
	}
	if got.SiSignoHuman != "SIGQUIT" {
		t.Errorf("SiSignoHuman = %q, want %q", got.SiSignoHuman, "SIGQUIT")
	}
	if got.SiSigno != 3 {
		t.Errorf("SiSigno = %d, want 3", got.SiSigno)
	}
}

func TestParseSignalIgnoresPanicMessageMentioningSignal(t *testing.T) {
	// A panic value that happens to contain the text "signal SIG..." must not
	// be misread as the runtime's own bracketed "[signal SIG…]" crash header.
	preamble := []string{"panic: aborted: received signal SIGTERM before drain"}
	if got := parseSignal(preamble); got != nil {
		t.Errorf("parseSignal(%v) = %+v, want nil", preamble, got)
	}
	if got := errorType(preamble, nil); got != "panic" {
		t.Errorf("errorType(%v, nil) = %q, want %q", preamble, got, "panic")
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

func TestErrorMessageMultilinePanic(t *testing.T) {
	// panic("first\nsecond") prints the panic value verbatim, so an embedded
	// newline in the panic value becomes a second physical preamble line
	// before the blank line that separates the preamble from the goroutine
	// stacks.
	preamble := []string{"panic: first", "second", ""}
	got := errorMessage(preamble, nil)
	want := "first\nsecond"
	if got != want {
		t.Errorf("errorMessage() = %q, want %q", got, want)
	}
}

func TestErrorMessageRecoveredNestedPanic(t *testing.T) {
	// A panic recovered and re-raised during deferred cleanup prints the
	// original panic, then the final one that actually terminated the
	// process, as additional preamble lines before the blank separator.
	// collectPanicMessage does not special-case this shape — it just
	// continues to the blank line — so this proves the final panic is no
	// longer silently dropped.
	preamble := []string{"panic: first", "\tpanic: second [recovered]", ""}
	got := errorMessage(preamble, nil)
	if !strings.Contains(got, "second") {
		t.Errorf("errorMessage() = %q, want it to contain the recovered panic %q", got, "second")
	}
}

func TestErrorMessageSingleLinePanicUnaffected(t *testing.T) {
	// A single-line panic followed directly by the goroutine stacks (no
	// intervening blank preamble line) must still return just that line.
	preamble := []string{"panic: boom"}
	got := errorMessage(preamble, nil)
	if got != "boom" {
		t.Errorf("errorMessage() = %q, want %q", got, "boom")
	}
}

func TestErrorTypeWindowsException(t *testing.T) {
	// Exact header format from runtime/signal_windows.go's winthrow, verified
	// against Go's own test suite (crash_test.go, signal_windows_test.go,
	// syscall_windows_test.go pin "Exception 0x80000003"/"0x2a"/"0xbad").
	tests := []string{
		"Exception 0xc0000005 0x0 0x18 0x7ff6a2345678",
		"Exception 0x80000003 0x0 0x0 0x7ff6a1b2c3d4",
		"Exception 0x2a 0x0 0x0 0x140001234",
	}
	for _, preambleLine := range tests {
		preamble := []string{preambleLine, "PC=0x7ff6a2345678"}
		got := errorType(preamble, nil)
		if got != "WindowsException" {
			t.Errorf("errorType(%q) = %q, want %q", preambleLine, got, "WindowsException")
		}
	}
}

func TestCapThreadsKeepsCrashedGoroutineWhenTruncating(t *testing.T) {
	threads := make([]Thread, maxReportThreads*2)
	// Mark a goroutine well past the cap as the crashed one, so retaining it
	// proves truncation is not just a prefix slice.
	threads[len(threads)-1].Crashed = true

	kept := capThreads(threads)

	if len(kept) != maxReportThreads {
		t.Fatalf("len(kept) = %d, want %d", len(kept), maxReportThreads)
	}
	crashed := 0
	for _, th := range kept {
		if th.Crashed {
			crashed++
		}
	}
	if crashed != 1 {
		t.Errorf("crashed goroutine count = %d, want exactly 1", crashed)
	}
	if !kept[0].Crashed {
		t.Error("crashed goroutine was not retained")
	}
}

func TestParseFramesCapsDeepStack(t *testing.T) {
	// Synthesize a goroutine stack far deeper than maxFramesPerThread, the
	// shape a real stack-overflow crash produces: one goroutine, thousands of
	// (function, location) line pairs. maxReportThreads (goroutine count)
	// does nothing to bound this — the cap under test is per-goroutine.
	const depth = maxFramesPerThread * 2
	lines := make([]string, 0, depth*2)
	for range depth {
		lines = append(lines,
			"main.recurse(0x1)",
			"\t/tmp/main.go:10 +0x20",
		)
	}

	frames, incomplete, consumed := parseFrames(lines)

	if len(frames) != maxFramesPerThread {
		t.Errorf("len(frames) = %d, want %d", len(frames), maxFramesPerThread)
	}
	if !incomplete {
		t.Error("incomplete = false, want true for a capped stack")
	}
	if consumed != len(lines) {
		t.Errorf("consumed = %d, want %d (all input lines, even past the cap)", consumed, len(lines))
	}
}

func TestParseSignalDoesNotPopulateSiCodeHuman(t *testing.T) {
	// Documents a known gap rather than masking it: si_code is signal- and
	// platform-specific (SEGV_MAPERR, BUS_ADRALN, FPE_INTDIV, ...), needing the
	// same kind of per-GOOS table this package already has for signalNumbers —
	// deliberately not built here without the same verification rigor that
	// went into signalNumbers, given how wrong an unverified one went for
	// SIGBUS. If this ever gets implemented, this test should start failing,
	// which is the correct prompt to update it alongside SigInfo's doc comment.
	dump := readFixture(t, "sigsegv.txt")
	r := parseCrashDump(dump)
	if r.SigInfo == nil {
		t.Fatal("SigInfo is nil")
	}
	if r.SigInfo.SiCodeHuman != "" {
		t.Errorf("SiCodeHuman = %q, want empty (not yet implemented)", r.SigInfo.SiCodeHuman)
	}
}

func TestParseFramesContinuesPastElisionMarker(t *testing.T) {
	// Shape of a real captured stack-overflow dump (runtime/traceback.go
	// prints the innermost 50 and outermost 50 logical frames and elides the
	// middle): a few inner frames, the elision marker, then the resumed
	// outer frames down to main.main/runtime.main. Trimmed to a handful of
	// frames on each side rather than the real dump's 16-million-frame gap.
	lines := []string{
		"main.recurse(0x0?)",
		"\t/tmp/main.go:10 +0x34",
		"main.recurse(0x0?)",
		"\t/tmp/main.go:10 +0x34",
		"...16777082 frames elided...",
		"main.recurse(0x20?)",
		"\t/tmp/main.go:10 +0x34",
		"main.main()",
		"\t/tmp/main.go:14 +0x24",
		"runtime.main()",
		"\t/usr/local/go/src/runtime/proc.go:290 +0x2b4",
	}

	frames, incomplete, consumed := parseFrames(lines)

	if !incomplete {
		t.Error("incomplete = false, want true: frames were elided")
	}
	if consumed != len(lines) {
		t.Errorf("consumed = %d, want %d (all lines, including those after the marker)", consumed, len(lines))
	}
	// 2 frames before the marker + 3 after (recurse, main.main, runtime.main):
	// the marker itself contributes no frame.
	if len(frames) != 5 {
		t.Fatalf("len(frames) = %d, want 5", len(frames))
	}
	last := frames[len(frames)-1]
	if last.Function != "runtime.main" {
		t.Errorf("last frame = %+v, want the resumed runtime.main frame to survive", last)
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

// TestParseCrashDumpCgoFault covers a fault that happened while Go was
// calling into C via cgo. The fixture is a real dump (trimmed of a few
// idle background goroutines), captured by actually crashing a cgo program
// under runtime/debug.SetCrashOutput, not hand-written. This dump shape
// differs from an ordinary Go-code SIGSEGV in three ways this test pins:
// the fault detail (PC=.../sigcode=.../addr=...) is on the line after the
// signal name rather than inline, the crashing goroutine's state is
// "syscall" rather than "running" (runtime/signal_unix.go's fatalsignal
// switches the reported goroutine to mp.curg, the Go code that entered the
// C call, whose state cgo tracks like a blocking syscall), and the runtime
// adds its own "signal arrived during cgo execution" line.
func TestParseCrashDumpCgoFault(t *testing.T) {
	dump := readFixture(t, "sigsegv_cgo.txt")
	r := parseCrashDump(dump)

	if r.Error.Type != "SIGSEGV" {
		t.Errorf("Error.Type = %q, want %q", r.Error.Type, "SIGSEGV")
	}
	if !strings.Contains(r.Error.Message, cgoExecutionMarker) {
		t.Errorf("Error.Message = %q, want it to contain %q", r.Error.Message, cgoExecutionMarker)
	}
	if r.Error.ThreadName != "goroutine 1" {
		t.Errorf("Error.ThreadName = %q, want %q (the goroutine that called into C, not index 0 by accident)", r.Error.ThreadName, "goroutine 1")
	}

	if r.SigInfo == nil {
		t.Fatal("SigInfo is nil")
	}
	if r.SigInfo.SiCode != 2 {
		t.Errorf("SigInfo.SiCode = %d, want 2 (from the second line's decimal sigcode=, not the bracketed form's hex code=)", r.SigInfo.SiCode)
	}
	if r.SigInfo.SiAddr != "0x0" {
		t.Errorf("SigInfo.SiAddr = %q, want %q", r.SigInfo.SiAddr, "0x0")
	}

	crashedCount := 0
	for _, th := range r.Error.Threads {
		if th.Crashed {
			crashedCount++
			if th.State != "syscall" {
				t.Errorf("crashed goroutine state = %q, want %q", th.State, "syscall")
			}
		}
	}
	if crashedCount != 1 {
		t.Errorf("crashed goroutine count = %d, want 1", crashedCount)
	}
}

func TestCrashingThreadFallsBackToSyscallState(t *testing.T) {
	// No goroutine is "running" — matches the real cgo-fault dump shape,
	// where the runtime reports the goroutine that called into C as
	// "syscall". The "syscall" one, not threads[0], must be picked even
	// when it is not first in dump order, proving this is a real state
	// match rather than an accidental index-0 default.
	threads := []Thread{
		{Name: "goroutine 2", State: "select (no cases)"},
		{Name: "goroutine 1", State: "syscall"},
		{Name: "goroutine 3", State: "sleep"},
	}

	got := crashingThread(threads)

	if got.Name != "goroutine 1" {
		t.Errorf("crashingThread() = %+v, want the syscall-state goroutine", got)
	}
	if !got.Crashed {
		t.Error("crashingThread() did not mark the returned thread Crashed")
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
