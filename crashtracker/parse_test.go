// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
			if want := strconv.Itoa(strconv.IntSize) + "-bit"; r.OSInfo.Bitness != want {
				t.Errorf("OSInfo.Bitness = %q, want %q", r.OSInfo.Bitness, want)
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

// TestCrashingThreadFallsBackWhenNoGoroutineIsRunning proves crashingThread's
// documented fallback: when no goroutine is in the "running" state -- a
// realistic shape for an operator-triggered SIGQUIT delivered while the
// process is otherwise idle, since SIGQUIT is a deliberate diagnostic dump
// request rather than a fault inside a specific goroutine's execution -- the
// first goroutine in dump order is still marked Crashed and used for
// error.stack, rather than the report ending up with no primary stack at
// all. See crashingThread's own doc comment for why this is a best-effort
// attribution, not evidence that goroutine actually caused anything.
func TestCrashingThreadFallsBackWhenNoGoroutineIsRunning(t *testing.T) {
	dump := []byte(`SIGQUIT: quit

goroutine 1 [chan receive]:
main.main()
	/tmp/crashdemo/main.go:20 +0x30

goroutine 5 [select]:
main.worker()
	/tmp/crashdemo/main.go:30 +0x1c
created by main.main in goroutine 1
	/tmp/crashdemo/main.go:25 +0x24
`)
	r := parseCrashDump(dump)

	if r.Error.ThreadName != "goroutine 1" {
		t.Errorf("Error.ThreadName = %q, want %q (the fallback: first goroutine in dump order)", r.Error.ThreadName, "goroutine 1")
	}
	crashedCount := 0
	for _, th := range r.Error.Threads {
		if th.Crashed {
			crashedCount++
		}
	}
	if crashedCount != 1 {
		t.Errorf("crashed goroutine count = %d, want exactly 1", crashedCount)
	}
	if r.SigInfo == nil || r.SigInfo.SiSignoHuman != "SIGQUIT" {
		t.Errorf("SigInfo = %+v, want a populated SIGQUIT SigInfo", r.SigInfo)
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
	// A panic recovered and then followed by another panic prints each panic
	// on its own line: the earlier ones first, the later ones indented with a
	// tab. Only the last panic ended the process, so its value is the message.
	for _, preamble := range [][]string{
		{"panic: first [recovered]", "\tpanic: second", ""},
		{"panic: first", "\tpanic: second [recovered]", ""},
		{"panic: first [recovered]", "\tpanic: second [recovered]", "\tpanic: third", ""},
	} {
		got := errorMessage(preamble, nil)
		want := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(preamble[len(preamble)-2]), "panic: "), " [recovered]")
		if got != want {
			t.Errorf("errorMessage(%q) = %q, want %q", preamble, got, want)
		}
	}
}

func TestErrorMessageSignalPanicKeepsOnlyPanicValue(t *testing.T) {
	preamble := []string{
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x2be9a0]",
		"",
	}
	sigInfo := parseSignal(preamble)
	got := errorMessage(preamble, sigInfo)
	want := "runtime error: invalid memory address or nil pointer dereference"
	if got != want {
		t.Errorf("errorMessage() = %q, want %q", got, want)
	}
}

func TestErrorMessageSignalWithoutPanicDropsRegisters(t *testing.T) {
	preamble := []string{
		"fatal error: unexpected signal during runtime execution",
		"[signal SIGSEGV: segmentation violation code=0x1 addr=0x8 pc=0x2be9a0]",
		"",
	}
	_, message := classifyError(preamble, nil, nil, parseSignal(preamble), stderrCrashLines{})
	want := "signal SIGSEGV: segmentation violation code=0x1 addr=0x8"
	if message != want {
		t.Errorf("message = %q, want %q", message, want)
	}
}

func TestParseCrashDumpWithStderr(t *testing.T) {
	for _, tt := range crashKinds {
		if tt.wantStderr == "" || tt.wantType != "runtime.plainError" {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			fd := readFixture(t, tt.name+"_fd_go126.txt")
			var scanner stderrLineScanner
			scanner.scan([]byte("application output\n"))
			scanner.scan(readFixture(t, tt.name+"_stderr_go126.txt"))
			r := parseCrashDumpWithStderr(fd, scanner.snapshot())
			want := tt.wantStderr
			if tt.name == "stack_exhaustion" {
				want = "stack overflow (goroutine stack exceeds 32768-byte limit)"
			}
			if r.Error.Type != "runtime.plainError" || r.Error.Message != want {
				t.Errorf("error = (%q, %q), want (runtime.plainError, %q)", r.Error.Type, r.Error.Message, want)
			}
		})
	}
}

func TestParseCrashDumpIgnoresStderrForPanic(t *testing.T) {
	// A fatal error line in the application's own output must not replace
	// the message of a normal panic.
	fd := readFixture(t, "panic_string_fd_go126.txt")
	r := parseCrashDumpWithStderr(fd, stderrCrashLines{fatal: "fatal error: logged by the application"})
	if r.Error.Type != "panic" || r.Error.Message != "boom" {
		t.Errorf("error = (%q, %q), want (panic, boom)", r.Error.Type, r.Error.Message)
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

// crashKinds lists the type and message that every Go crash kind reports. The
// fd message is for the crash output file. The stderr message is for the text
// that the runtime writes to stderr, which the supervisor tees. An empty stderr
// message means the kind has no stderr fixture because stderr adds nothing.
var crashKinds = []struct {
	name          string
	wantType      string
	wantFDMessage string
	wantStderr    string
}{
	{"concurrent_map_write", "runtime.plainError", "fatal error in main.writeMap" + missingFatalMessage, "concurrent map writes"},
	{"concurrent_map_read_write", "runtime.plainError", "fatal error in main.crash" + missingFatalMessage, "concurrent map read and map write"},
	{"deadlock", "runtime.plainError", "possible deadlock" + missingFatalMessage, "all goroutines are asleep - deadlock!"},
	{"stack_exhaustion", "runtime.plainError", "stack overflow", "stack overflow"},
	{"mutex_unlock", "runtime.plainError", "fatal error in main.crash" + missingFatalMessage, "sync: unlock of unlocked mutex"},
	{"rwmutex_runlock", "runtime.plainError", "fatal error in main.crash" + missingFatalMessage, "sync: RUnlock of unlocked RWMutex"},
	{"nil_deref", "SIGSEGV", "runtime error: invalid memory address or nil pointer dereference", "runtime error: invalid memory address or nil pointer dereference"},
	{"repanic", "panic", "repanic after: first", ""},
	{"panic_struct", "panic", "(main.point)", ""},
	{"panic_string", "panic", "boom", ""},
	{"panic_error", "panic", "boom error", ""},
	{"panic_wrapped_error", "panic", "wrapped: inner error", ""},
	{"panic_custom_error", "panic", "custom error 7", ""},
	{"panic_int", "panic", "42", ""},
	{"panic_nil", "panic", "panic called with nil argument", ""},
	{"index_out_of_range", "panic", "runtime error: index out of range [118] with length 3", ""},
	{"nil_map_write", "panic", "assignment to entry in nil map", "assignment to entry in nil map"},
	{"send_closed_channel", "panic", "send on closed channel", ""},
	{"close_closed_channel", "panic", "close of closed channel", "close of closed channel"},
	{"type_assertion", "panic", "interface conversion: interface {} is string, not int", ""},
	{"divide_by_zero", "panic", "runtime error: integer divide by zero", ""},
	{"panic_goroutine", "panic", "goroutine boom", ""},
}

const missingFatalMessage = " (message not in crash output, golang/go#76859)"

func TestParseCrashDumpCrashOutputFixtures(t *testing.T) {
	volatile := regexp.MustCompile(`0x[0-9a-fA-F]+|\b(pc|sp|fp)=`)
	for _, tt := range crashKinds {
		t.Run(tt.name, func(t *testing.T) {
			for _, shape := range []string{"fd", "stderr"} {
				wantMessage := tt.wantFDMessage
				if shape == "stderr" {
					if tt.wantStderr == "" {
						continue
					}
					wantMessage = tt.wantStderr
				}
				t.Run(shape, func(t *testing.T) {
					r := parseCrashDump(readFixture(t, tt.name+"_"+shape+"_go126.txt"))
					if r.Error.Type != tt.wantType {
						t.Errorf("Error.Type = %q, want %q", r.Error.Type, tt.wantType)
					}
					if r.Error.Message != wantMessage {
						t.Errorf("Error.Message = %q, want %q", r.Error.Message, wantMessage)
					}
					if volatile.MatchString(r.Error.Message) {
						t.Errorf("Error.Message = %q, want no address, pc, sp or fp", r.Error.Message)
					}
					if tt.name == "nil_deref" {
						if r.SigInfo == nil || r.SigInfo.SiSignoHuman != "SIGSEGV" || r.SigInfo.SiSigno != 11 || r.SigInfo.SiCode != 1 || r.SigInfo.SiAddr != "0x0" {
							t.Errorf("SigInfo = %+v, want SIGSEGV with code 1 and address 0x0", r.SigInfo)
						}
					} else if r.SigInfo != nil {
						t.Errorf("SigInfo = %+v, want nil", r.SigInfo)
					}
					if r.Error.Stack == nil || len(r.Error.Stack.Frames) == 0 {
						t.Fatal("Error.Stack has no frames")
					}
				})
			}
		})
	}
}

func TestParseCrashDumpFatalFrames(t *testing.T) {
	for _, fn := range []string{"runtime.throw", "runtime.fatal", "runtime.fatalthrow", "runtime.fatalpanic", "internal/runtime/maps.fatal"} {
		t.Run(fn, func(t *testing.T) {
			dump := "\ngoroutine 12 [running]:\n" + fn + "(0x1234)\n\t/runtime/panic.go:1 +0x20\nmain.writeMap(0x5678)\n\t/app/main.go:10 +0x10\n"
			r := parseCrashDump([]byte(dump))
			if r.Error.Type != "runtime.plainError" {
				t.Errorf("Error.Type = %q, want runtime.plainError", r.Error.Type)
			}
			want := "fatal error in main.writeMap (message not in crash output, golang/go#76859)"
			if r.Error.Message != want {
				t.Errorf("Error.Message = %q, want %q", r.Error.Message, want)
			}
		})
	}
}

func TestParseCrashDumpFatalFramesWithoutUserFrame(t *testing.T) {
	for _, dump := range []string{
		"\ngoroutine 1 [running]:\nruntime.throw(0x1)\n\t/runtime/panic.go:1229\n",
		"\nruntime stack:\nruntime.throw(0x1)\n\t/runtime/panic.go:1229\n",
	} {
		r := parseCrashDump([]byte(dump))
		want := "fatal error (message not in crash output, golang/go#76859)"
		if r.Error.Type != "runtime.plainError" || r.Error.Message != want {
			t.Errorf("error = (%q, %q), want (runtime.plainError, %q)", r.Error.Type, r.Error.Message, want)
		}
	}
}

func TestParseCrashDumpUnknownPreambleIsNotDeadlock(t *testing.T) {
	dump := []byte("unknown preamble\n\ngoroutine 1 [select (no cases)]:\nmain.main()\n\t/app/main.go:10\n")
	r := parseCrashDump(dump)
	if r.Error.Type != "runtime.plainError" || r.Error.Message != "unknown preamble" {
		t.Errorf("error = (%q, %q), want (runtime.plainError, unknown preamble)", r.Error.Type, r.Error.Message)
	}
}

func TestParseCrashDumpHeaderlessRunningIsNotDeadlock(t *testing.T) {
	dump := []byte("\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:10\n")
	r := parseCrashDump(dump)
	want := "fatal error in main.main" + missingFatalMessage
	if r.Error.Type != "runtime.plainError" || r.Error.Message != want {
		t.Errorf("error = (%q, %q), want (runtime.plainError, %q)", r.Error.Type, r.Error.Message, want)
	}
}

func TestParseCrashDumpHeaderlessRunningLockedToThread(t *testing.T) {
	dump := []byte(`
goroutine 1 [chan receive]:
main.main()
	/app/main.go:10

goroutine 2 [running, locked to thread]:
sync.(*Mutex).Unlock(...)
	/runtime/sync/mutex.go:65
main.crash()
	/app/main.go:20
`)
	r := parseCrashDump(dump)
	want := "fatal error in main.crash" + missingFatalMessage
	if r.Error.Type != "runtime.plainError" || r.Error.Message != want {
		t.Errorf("error = (%q, %q), want (runtime.plainError, %q)", r.Error.Type, r.Error.Message, want)
	}
	if len(r.Error.Threads) != 2 {
		t.Fatalf("len(Error.Threads) = %d, want 2", len(r.Error.Threads))
	}
	for _, thread := range r.Error.Threads {
		if want := thread.Name == "goroutine 2"; thread.Crashed != want {
			t.Errorf("%s Crashed = %t, want %t", thread.Name, thread.Crashed, want)
		}
	}
}

func TestParseCrashDumpPreambleOverridesFatalFrames(t *testing.T) {
	for _, tt := range []struct {
		preamble string
		wantType string
		wantMsg  string
	}{
		{"panic: original panic\n", "panic", "original panic"},
		{"fatal error: original fatal\n", "runtime.plainError", "original fatal"},
		{"SIGABRT: abort\n", "SIGABRT", "SIGABRT: abort"},
		{"Exception 0xc0000005 0x0 0x18 0x1234\n", "WindowsException", "Exception 0xc0000005 0x0 0x18 0x1234"},
	} {
		t.Run(tt.wantType, func(t *testing.T) {
			dump := tt.preamble + "\ngoroutine 1 [running]:\nruntime.fatalpanic(0x1)\n\t/runtime/panic.go:1456\nmain.main()\n\t/app/main.go:10\n"
			r := parseCrashDump([]byte(dump))
			if r.Error.Type != tt.wantType || r.Error.Message != tt.wantMsg {
				t.Errorf("error = (%q, %q), want (%q, %q)", r.Error.Type, r.Error.Message, tt.wantType, tt.wantMsg)
			}
		})
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
