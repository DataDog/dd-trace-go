// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The tests in this file characterize the current behavior of parseCrashDump
// through its public result only. They deliberately avoid parseFrames,
// splitDump and the other parser internals so that they keep working when the
// goroutine and frame parser is replaced. Where the behavior looks like a bug
// or a gap, the test still pins it and the case carries a "KNOWN GAP" comment.

// fixtureGOROOT is the toolchain directory in the real_* and synthetic_*
// captures. They were taken with go1.27.4 from Homebrew on darwin/arm64.
const fixtureGOROOT = "/opt/homebrew/Cellar/go/1.27.4/libexec"

// TestParseCrashDumpFixtures pins the headline facts of each fixture. The
// golden files in testdata pin everything else.
//
// How each real_* fixture was captured (all with the local go1.27.4, running
// the built binary, not "go run", so there is no "exit status" trailer; the
// sources are small programs under /tmp/crashdemo/<name>/main.go):
//
//	real_panic_traceback_system.txt: GOTRACEBACK=system ./nilptr.bin
//	  nil pointer dereference in a method, with blocked goroutines (chan
//	  receive, select, sleep, mutex). Gives the extended header
//	  "goroutine N gp=0x... m=... mp=... [state]:" and the
//	  "fp=0x... sp=0x... pc=0x..." location suffix.
//	real_sigabrt_traceback_crash.txt: GOTRACEBACK=crash ./fatalsig.bin
//	  the program sends SIGABRT to itself. Gives the top-level
//	  "SIGABRT: abort" header, goroutine 0 blocks, a SIGQUIT block per
//	  other thread and the register dumps.
//	real_tracebackancestors.txt: GOTRACEBACK=all GODEBUG=tracebackancestors=5 ./ancestors.bin
//	  a nil map write in a goroutine spawned from a goroutine spawned from
//	  main. Gives "[originating from goroutine N]:" blocks.
//	real_created_by.txt: GOTRACEBACK=all ./createdby.bin
//	  index out of range with several goroutines spawned from main.
//	  Gives "created by X in goroutine N" lines.
//	real_goroutine_labels.txt: GOTRACEBACK=all GODEBUG=tracebacklabels=1 ./labels.bin
//	  a nil map write while another goroutine runs under pprof labels.
//	  Gives the "goroutine N [state] {key: value}:" header.
//
// What each synthetic_* fixture simulates (hand-edited from the real
// captures above):
//
//	synthetic_state_suffixes.txt: header suffixes from goroutineheader in
//	  runtime/traceback.go (go1.27.4): ", N minutes", ", locked to thread",
//	  " (leaked)", " (durable)" and ", synctest bubble N", in the runtime's
//	  order.
//	synthetic_frames_elided.txt: the "...N frames elided..." marker of a
//	  stack overflow, with a leading "runtime stack:" block.
//	synthetic_windows_paths.txt: a Windows panic: "C:/..." paths (the Go
//	  runtime prints forward slashes), a path with a space, and the
//	  "[signal 0xc0000005 ...]" line.
//	synthetic_truncated_function_line.txt: dump cut after a function line,
//	  before its location line, with no trailing newline.
//	synthetic_truncated_location_line.txt: dump cut inside a location line,
//	  with no trailing newline.
func TestParseCrashDumpFixtures(t *testing.T) {
	tests := []struct {
		fixture string

		wantType    string
		wantMessage string
		wantSigName string // empty means SigInfo must be nil

		// wantStates holds the State of every thread, verbatim and in dump order.
		wantStates []string

		wantCrashedName   string
		wantCrashedFrames int
		wantCrashedTop    Frame
		wantStackIncompl  bool // Error.Stack.Incomplete, the crashed goroutine's stack
		wantIncompleteN   int  // number of threads with Stack.Incomplete set
	}{
		{
			fixture:           "close_closed_channel.txt",
			wantType:          "panic",
			wantMessage:       "close of closed channel",
			wantStates:        []string{"running"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 1,
			wantCrashedTop:    Frame{Function: "main.main", File: "/tmp/crashtracker-fixtures/close_closed_channel.go", Line: 6},
		},
		{
			fixture:           "concurrent_map_write.txt",
			wantType:          "runtime.plainError",
			wantMessage:       "concurrent map writes",
			wantStates:        []string{"running", "running"},
			wantCrashedName:   "goroutine 35",
			wantCrashedFrames: 2,
			wantCrashedTop:    Frame{Function: "internal/runtime/maps.fatal", File: "/opt/homebrew/Cellar/go/1.26.5/libexec/src/runtime/panic.go", Line: 1181},
		},
		{
			fixture:           "deadlock.txt",
			wantType:          "runtime.plainError",
			wantMessage:       "all goroutines are asleep - deadlock!",
			wantStates:        []string{"chan receive"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 1,
			wantCrashedTop:    Frame{Function: "main.main", File: "/tmp/crashtracker-fixtures/deadlock.go", Line: 5},
		},
		{
			fixture:           "panic_simple.txt",
			wantType:          "panic",
			wantMessage:       "something went wrong",
			wantStates:        []string{"running", "select (no cases)", "chan receive"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 4,
			wantCrashedTop:    Frame{Function: "panic", File: "/opt/homebrew/Cellar/go/1.26.5/libexec/src/runtime/panic.go", Line: 830},
		},
		{
			fixture:           "panic_traceback_all.txt",
			wantType:          "panic",
			wantMessage:       "traceback all fixture",
			wantStates:        []string{"running", "chan receive"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 1,
			wantCrashedTop:    Frame{Function: "main.main", File: "/tmp/crashtracker-fixtures/panic_traceback_all.go", Line: 8},
		},
		{
			// KNOWN GAP: for a signal crash the message is the "[signal ...]"
			// line, so the panic text ("invalid memory address or nil pointer
			// dereference") is not in Error.Message.
			fixture:           "sigsegv.txt",
			wantType:          "SIGSEGV",
			wantMessage:       "signal SIGSEGV: segmentation violation code=0x2 addr=0x0 pc=0x1000a79b4",
			wantSigName:       "SIGSEGV",
			wantStates:        []string{"running", "select (no cases)", "sleep"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 2,
			wantCrashedTop:    Frame{Function: "main.deref", File: "/tmp/crashdemo/main.go", Line: 7},
		},
		{
			fixture:     "stack_exhaustion.txt",
			wantType:    "runtime.plainError",
			wantMessage: "stack overflow",
			wantStates: []string{
				"running", "force gc (idle)", "GC sweep wait", "GC scavenge wait",
				"GOMAXPROCS updater (idle)", "finalizer wait",
			},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 35,
			wantCrashedTop:    Frame{Function: "main.recurse", File: "/tmp/crashtracker-fixtures/stack_exhaustion.go", Line: 5},
		},
		{
			// GOTRACEBACK=system: extended headers and fp/sp/pc suffixes. The
			// first location line of the crashed stack is the runtime's panic
			// frame. Inlined frames such as main.(*server).Serve have no
			// "+0x" offset.
			//
			// KNOWN GAP: same as sigsegv.txt, the message is the "[signal ...]"
			// line.
			fixture:     "real_panic_traceback_system.txt",
			wantType:    "SIGSEGV",
			wantMessage: "signal SIGSEGV: segmentation violation code=0x2 addr=0x0 pc=0x104d6ec44",
			wantSigName: "SIGSEGV",
			wantStates: []string{
				"running", "force gc (idle)", "GC sweep wait", "GC scavenge wait",
				"GOMAXPROCS updater (idle)", "chan receive", "select (no cases)", "sleep", "sync.Mutex.Lock",
			},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 7,
			wantCrashedTop:    Frame{Function: "panic", File: fixtureGOROOT + "/src/runtime/panic.go", Line: 878},
		},
		{
			// GOTRACEBACK=crash with an external SIGABRT.
			//
			// KNOWN GAP: no goroutine is "running", so the crashed thread is
			// the first one in the dump, "goroutine 0 [idle]" (the g0 of the
			// thread that took the signal). The user goroutine that called
			// kill (goroutine 1, "syscall") is not selected.
			//
			// KNOWN GAP: the dump has one "goroutine 0" block per runtime
			// thread, so four threads share the name "goroutine 0".
			//
			// KNOWN GAP: the register dump ("r0      0x104", ...) directly
			// follows the last frame of each of those four blocks. The parser
			// reads the first register line as a function line without a
			// location line, so it marks those four threads Incomplete even
			// though all their frames were parsed. The register dump of the
			// first block is separated by a blank line and is ignored.
			fixture:     "real_sigabrt_traceback_crash.txt",
			wantType:    "SIGABRT",
			wantMessage: "SIGABRT: abort",
			wantSigName: "SIGABRT",
			wantStates: []string{
				"idle", "syscall", "force gc (idle)", "GC sweep wait", "GC scavenge wait",
				"GOMAXPROCS updater (idle)", "finalizer wait", "chan receive", "sleep",
				"idle", "idle", "idle", "idle",
			},
			wantCrashedName:   "goroutine 0",
			wantCrashedFrames: 7,
			wantCrashedTop:    Frame{Function: "syscall.syscalln", File: fixtureGOROOT + "/src/runtime/sys_darwin.go", Line: 27},
			wantIncompleteN:   4,
		},
		{
			// KNOWN GAP: "[originating from goroutine N]:" blocks are not
			// understood. The line is read as a function line, the frame
			// after it as a malformed location, and the rest of the
			// goroutine's block is dropped. The crashed goroutine keeps only
			// the frames before its "created by" line (1 of its frames here)
			// and is marked Incomplete, as are the other two goroutines that
			// have ancestors. Goroutine 1 has none and is complete.
			fixture:           "real_tracebackancestors.txt",
			wantType:          "panic",
			wantMessage:       "assignment to entry in nil map",
			wantStates:        []string{"running", "runnable", "sleep", "sleep"},
			wantCrashedName:   "goroutine 6",
			wantCrashedFrames: 1,
			wantCrashedTop:    Frame{Function: "main.level3", File: "/tmp/crashdemo/ancestors/main.go", Line: 12},
			wantStackIncompl:  true,
			wantIncompleteN:   3,
		},
		{
			// "created by X in goroutine N" lines are skipped and do not
			// count as frames.
			fixture:           "real_created_by.txt",
			wantType:          "panic",
			wantMessage:       "runtime error: index out of range [5] with length 0",
			wantStates:        []string{"running", "sleep", "sleep", "sleep", "chan receive (nil chan)"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 1,
			wantCrashedTop:    Frame{Function: "main.main", File: "/tmp/crashdemo/createdby/main.go", Line: 27},
		},
		{
			// KNOWN GAP: with GODEBUG=tracebacklabels=1 the header ends with
			// "] {handler: checkout, ...}:", which goroutineHeaderRe does not
			// match. Goroutine 5 is not reported at all: one thread, no
			// Incomplete flag, no sign that anything was dropped.
			fixture:           "real_goroutine_labels.txt",
			wantType:          "panic",
			wantMessage:       "assignment to entry in nil map",
			wantStates:        []string{"running"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 1,
			wantCrashedTop:    Frame{Function: "main.main", File: "/tmp/crashdemo/labels/main.go", Line: 18},
		},
		{
			// The State string is everything between "[" and "]:", verbatim.
			//
			// KNOWN GAP: crashingThread compares State with "running"
			// exactly. The real crashing goroutine 7 is "running, locked to
			// thread", so it is not recognized. No goroutine matches, and the
			// first goroutine in the dump ("goroutine 1", blocked in chan
			// receive) is reported as the crashed one.
			fixture:     "synthetic_state_suffixes.txt",
			wantType:    "SIGSEGV",
			wantMessage: "signal SIGSEGV: segmentation violation code=0x1 addr=0x18 pc=0x4a1b2c",
			wantSigName: "SIGSEGV",
			wantStates: []string{
				"chan receive, 6883 minutes",
				"running, locked to thread",
				"select, 12 minutes, locked to thread",
				"chan receive (leaked)",
				"syscall, 3 minutes",
				"chan receive (durable), synctest bubble 1",
			},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 2,
			wantCrashedTop:    Frame{Function: "runtime.gopark", File: fixtureGOROOT + "/src/runtime/proc.go", Line: 474},
		},
		{
			// The elision marker is not a frame. Frames after it are kept,
			// and the stack is marked Incomplete. The leading "runtime
			// stack:" block is part of the preamble and is discarded.
			fixture:           "synthetic_frames_elided.txt",
			wantType:          "runtime.plainError",
			wantMessage:       "stack overflow",
			wantStates:        []string{"running"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 7,
			wantCrashedTop:    Frame{Function: "main.recurse", File: "/tmp/crashdemo/elided/main.go", Line: 10},
			wantStackIncompl:  true,
			wantIncompleteN:   1,
		},
		{
			// Drive letters, forward slashes and a space in the path are
			// kept as they are, and the line number is split at the last
			// colon.
			//
			// KNOWN GAP: a Windows panic prints "[signal 0xc0000005 ...]",
			// not "[signal SIGSEGV ...]", so SigInfo stays nil and the line
			// becomes the second line of the message.
			fixture:           "synthetic_windows_paths.txt",
			wantType:          "panic",
			wantMessage:       "runtime error: invalid memory address or nil pointer dereference\n[signal 0xc0000005 code=0x0 addr=0x18 pc=0x7ff6a2345678]",
			wantStates:        []string{"running", "chan receive"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 4,
			wantCrashedTop:    Frame{Function: "main.(*Server).Serve", File: "C:/Users/x/main.go", Line: 12},
		},
		{
			// A function line without a location line at the end of the
			// dump: that function is dropped, the frames before it are kept,
			// and the stack is Incomplete.
			fixture:           "synthetic_truncated_function_line.txt",
			wantType:          "panic",
			wantMessage:       "boom",
			wantStates:        []string{"running"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 3,
			wantCrashedTop:    Frame{Function: "panic", File: "/tmp/crashdemo/trunc/main.go", Line: 7},
			wantStackIncompl:  true,
			wantIncompleteN:   1,
		},
		{
			// KNOWN GAP: a dump cut inside a location line is not detected.
			// See TestParseCrashDumpTruncatedLocationLine for the frame.
			fixture:           "synthetic_truncated_location_line.txt",
			wantType:          "panic",
			wantMessage:       "boom",
			wantStates:        []string{"running", "chan receive"},
			wantCrashedName:   "goroutine 1",
			wantCrashedFrames: 2,
			wantCrashedTop:    Frame{Function: "panic", File: "/tmp/crashdemo/trunc/main.go", Line: 7},
		},
	}

	for _, tt := range tests {
		t.Run(strings.TrimSuffix(tt.fixture, ".txt"), func(t *testing.T) {
			r := parseCrashDump(readFixture(t, tt.fixture))

			if r.Error.Type != tt.wantType {
				t.Errorf("Error.Type = %q, want %q", r.Error.Type, tt.wantType)
			}
			if r.Error.Message != tt.wantMessage {
				t.Errorf("Error.Message = %q, want %q", r.Error.Message, tt.wantMessage)
			}
			switch {
			case tt.wantSigName == "" && r.SigInfo != nil:
				t.Errorf("SigInfo = %+v, want nil", r.SigInfo)
			case tt.wantSigName != "" && (r.SigInfo == nil || r.SigInfo.SiSignoHuman != tt.wantSigName):
				t.Errorf("SigInfo = %+v, want signal %s", r.SigInfo, tt.wantSigName)
			}

			var gotStates []string
			crashedThreads := 0
			incompleteThreads := 0
			for _, th := range r.Error.Threads {
				gotStates = append(gotStates, th.State)
				if th.Crashed {
					crashedThreads++
				}
				if th.Stack.Incomplete {
					incompleteThreads++
				}
			}
			if !slices.Equal(gotStates, tt.wantStates) {
				t.Errorf("thread states = %q, want %q", gotStates, tt.wantStates)
			}
			if crashedThreads != 1 {
				t.Errorf("crashed thread count = %d, want 1", crashedThreads)
			}
			if incompleteThreads != tt.wantIncompleteN {
				t.Errorf("incomplete thread count = %d, want %d", incompleteThreads, tt.wantIncompleteN)
			}

			if r.Error.ThreadName != tt.wantCrashedName {
				t.Errorf("Error.ThreadName = %q, want %q", r.Error.ThreadName, tt.wantCrashedName)
			}
			stack := r.Error.Stack
			if stack == nil {
				t.Fatal("Error.Stack is nil")
			}
			if len(stack.Frames) != tt.wantCrashedFrames {
				t.Fatalf("len(Error.Stack.Frames) = %d, want %d", len(stack.Frames), tt.wantCrashedFrames)
			}
			if got := stack.Frames[0]; got != tt.wantCrashedTop {
				t.Errorf("top frame = %+v, want %+v", got, tt.wantCrashedTop)
			}
			if stack.Incomplete != tt.wantStackIncompl {
				t.Errorf("Error.Stack.Incomplete = %t, want %t", stack.Incomplete, tt.wantStackIncompl)
			}
			if reportIncomplete(r) != tt.wantStackIncompl {
				t.Errorf("reportIncomplete = %t, want %t", reportIncomplete(r), tt.wantStackIncompl)
			}

			// error.stack must equal the crashed thread's own stack.
			for _, th := range r.Error.Threads {
				if th.Crashed && !slices.Equal(th.Stack.Frames, stack.Frames) {
					t.Errorf("crashed thread %q frames differ from Error.Stack.Frames", th.Name)
				}
			}
		})
	}
}

func TestParseCrashDumpTruncatedLocationLine(t *testing.T) {
	r := parseCrashDump(readFixture(t, "synthetic_truncated_location_line.txt"))

	if len(r.Error.Threads) != 2 {
		t.Fatalf("len(Threads) = %d, want 2", len(r.Error.Threads))
	}
	worker := r.Error.Threads[1]
	if worker.Name != "goroutine 6" || len(worker.Stack.Frames) != 2 {
		t.Fatalf("thread = %q with %d frames, want goroutine 6 with 2 frames", worker.Name, len(worker.Stack.Frames))
	}
	// KNOWN GAP: the cut location line "\t.../runtime/asm_ar" starts with a tab,
	// so it counts as a location. It becomes a frame with a truncated file
	// and line 0, and the stack is not marked Incomplete.
	want := Frame{Function: "runtime.goexit", File: fixtureGOROOT + "/src/runtime/asm_ar", Line: 0}
	if got := worker.Stack.Frames[1]; got != want {
		t.Errorf("last frame = %+v, want %+v", got, want)
	}
	if worker.Stack.Incomplete {
		t.Error("goroutine 6 Stack.Incomplete = true, want false (gap: the cut is not detected)")
	}
}

// goroutineDump builds a dump with a "panic: boom" preamble and the given
// goroutine blocks.
func goroutineDump(blocks ...string) []byte {
	return []byte("panic: boom\n\n" + strings.Join(blocks, "\n"))
}

// goroutineBlock builds one goroutine block with n identical frames.
func goroutineBlock(id int, state string, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "goroutine %d [%s]:\n", id, state)
	for range n {
		b.WriteString("main.recurse(0x1)\n\t/tmp/main.go:10 +0x20\n")
	}
	return b.String()
}

func TestParseCrashDumpCapsFramesPerThread(t *testing.T) {
	tests := []struct {
		name           string
		frames         int
		wantFrames     int
		wantIncomplete bool
	}{
		{name: "below cap", frames: maxFramesPerThread - 1, wantFrames: maxFramesPerThread - 1},
		{name: "at cap", frames: maxFramesPerThread, wantFrames: maxFramesPerThread},
		{name: "one over cap", frames: maxFramesPerThread + 1, wantFrames: maxFramesPerThread, wantIncomplete: true},
		{name: "twice the cap", frames: 2 * maxFramesPerThread, wantFrames: maxFramesPerThread, wantIncomplete: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A second goroutine after the deep one proves the parser still
			// finds the next header after skipping the frames above the cap.
			r := parseCrashDump(goroutineDump(
				goroutineBlock(1, "running", tt.frames),
				goroutineBlock(2, "chan receive", 1),
			))

			if len(r.Error.Threads) != 2 {
				t.Fatalf("len(Threads) = %d, want 2", len(r.Error.Threads))
			}
			deep := r.Error.Threads[0]
			if len(deep.Stack.Frames) != tt.wantFrames {
				t.Errorf("deep thread frames = %d, want %d", len(deep.Stack.Frames), tt.wantFrames)
			}
			if deep.Stack.Incomplete != tt.wantIncomplete {
				t.Errorf("deep thread Incomplete = %t, want %t", deep.Stack.Incomplete, tt.wantIncomplete)
			}
			if r.Error.Stack == nil || len(r.Error.Stack.Frames) != tt.wantFrames || r.Error.Stack.Incomplete != tt.wantIncomplete {
				t.Errorf("Error.Stack = %+v, want %d frames and Incomplete=%t", r.Error.Stack, tt.wantFrames, tt.wantIncomplete)
			}
			// The cap keeps the innermost frames.
			if first := deep.Stack.Frames[0]; first != (Frame{Function: "main.recurse", File: "/tmp/main.go", Line: 10}) {
				t.Errorf("first frame = %+v", first)
			}
			next := r.Error.Threads[1]
			if next.Name != "goroutine 2" || len(next.Stack.Frames) != 1 || next.Stack.Incomplete {
				t.Errorf("thread after the deep one = %q with %d frames, Incomplete=%t; want goroutine 2, 1 frame, complete",
					next.Name, len(next.Stack.Frames), next.Stack.Incomplete)
			}
		})
	}
}

func TestParseCrashDumpCapsReportThreads(t *testing.T) {
	const total = 2 * maxReportThreads

	// The crashed goroutine is the last one in the dump, far beyond the cap.
	blocks := make([]string, 0, total)
	for id := 1; id < total; id++ {
		blocks = append(blocks, goroutineBlock(id, "chan receive", 1))
	}
	blocks = append(blocks, goroutineBlock(total, "running", 1))
	r := parseCrashDump(goroutineDump(blocks...))

	threads := r.Error.Threads
	if len(threads) != maxReportThreads {
		t.Fatalf("len(Threads) = %d, want %d", len(threads), maxReportThreads)
	}
	// The crashed goroutine is hoisted to the front, the others keep dump order.
	wantName := fmt.Sprintf("goroutine %d", total)
	if !threads[0].Crashed || threads[0].Name != wantName || threads[0].State != "running" {
		t.Errorf("threads[0] = %+v, want the crashed %s", threads[0], wantName)
	}
	for i := 1; i < len(threads); i++ {
		if want := fmt.Sprintf("goroutine %d", i); threads[i].Name != want || threads[i].Crashed {
			t.Fatalf("threads[%d] = %q crashed=%t, want %q not crashed", i, threads[i].Name, threads[i].Crashed, want)
		}
	}
	if r.Error.ThreadName != wantName {
		t.Errorf("Error.ThreadName = %q, want %q", r.Error.ThreadName, wantName)
	}
	if r.Error.Stack == nil || len(r.Error.Stack.Frames) != 1 {
		t.Errorf("Error.Stack = %+v, want the crashed goroutine's one frame", r.Error.Stack)
	}

	t.Run("at cap keeps dump order", func(t *testing.T) {
		blocks := make([]string, 0, maxReportThreads)
		for id := 1; id < maxReportThreads; id++ {
			blocks = append(blocks, goroutineBlock(id, "chan receive", 1))
		}
		blocks = append(blocks, goroutineBlock(maxReportThreads, "running", 1))
		r := parseCrashDump(goroutineDump(blocks...))

		if len(r.Error.Threads) != maxReportThreads {
			t.Fatalf("len(Threads) = %d, want %d", len(r.Error.Threads), maxReportThreads)
		}
		last := r.Error.Threads[maxReportThreads-1]
		if !last.Crashed || last.Name != fmt.Sprintf("goroutine %d", maxReportThreads) {
			t.Errorf("last thread = %q crashed=%t, want the crashed goroutine to stay last", last.Name, last.Crashed)
		}
		if r.Error.Threads[0].Name != "goroutine 1" {
			t.Errorf("first thread = %q, want goroutine 1", r.Error.Threads[0].Name)
		}
	})
}

func TestParseCrashDumpDegenerateInput(t *testing.T) {
	tests := []struct {
		name        string
		dump        []byte
		wantType    string
		wantMessage string
		wantSigName string
		// wantThread is the only expected thread, if any. These inputs
		// produce zero or one thread.
		wantThread *Thread
	}{
		{
			// The default error type is "panic" even when there is nothing to
			// classify. There is no crashed thread, so error.stack is nil,
			// and reportIncomplete reports true.
			name:     "empty input",
			dump:     []byte{},
			wantType: "panic",
		},
		{
			name:     "nil input",
			dump:     nil,
			wantType: "panic",
		},
		{
			name:        "preamble only",
			dump:        []byte("panic: boom\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x1]\n\nsome trailing text\n"),
			wantType:    "SIGSEGV",
			wantMessage: "signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x1",
			wantSigName: "SIGSEGV",
		},
		{
			name:        "preamble only without signal",
			dump:        []byte("fatal error: all goroutines are asleep - deadlock!\n"),
			wantType:    "runtime.plainError",
			wantMessage: "all goroutines are asleep - deadlock!",
		},
		{
			// Text that is not a goroutine header falls back to the first
			// non-empty line as the message.
			name:        "garbage",
			dump:        []byte("\n  hello\nworld\n"),
			wantType:    "panic",
			wantMessage: "hello",
		},
		{
			// A header without frames: one thread, no frames, Incomplete.
			// Frames is nil, so it serializes as null.
			name:        "header with zero frames",
			dump:        []byte("panic: boom\n\ngoroutine 1 [running]:\n"),
			wantType:    "panic",
			wantMessage: "boom",
			wantThread: &Thread{
				Crashed: true,
				Name:    "goroutine 1",
				State:   "running",
				Stack:   StackTrace{Format: stackFormat, Incomplete: true},
			},
		},
		{
			// KNOWN GAP: a crash dump cut right after the header looks the
			// same as a header the runtime printed for a goroutine with no
			// frames. Both are Incomplete, which is the right call for the
			// crashed goroutine only.
			name:        "header followed by blank line only",
			dump:        []byte("panic: boom\n\ngoroutine 1 [running]:\n\n"),
			wantType:    "panic",
			wantMessage: "boom",
			wantThread: &Thread{
				Crashed: true,
				Name:    "goroutine 1",
				State:   "running",
				Stack:   StackTrace{Format: stackFormat, Incomplete: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := parseCrashDump(tt.dump)
			if r == nil {
				t.Fatal("parseCrashDump returned nil")
			}
			if r.Error.Type != tt.wantType {
				t.Errorf("Error.Type = %q, want %q", r.Error.Type, tt.wantType)
			}
			if r.Error.Message != tt.wantMessage {
				t.Errorf("Error.Message = %q, want %q", r.Error.Message, tt.wantMessage)
			}
			if !r.Error.IsCrash || r.Error.SourceType != "Crashtracking" || r.DDSource != "crashtracker" {
				t.Errorf("report envelope = IsCrash:%t SourceType:%q DDSource:%q", r.Error.IsCrash, r.Error.SourceType, r.DDSource)
			}
			if tt.wantSigName == "" && r.SigInfo != nil {
				t.Errorf("SigInfo = %+v, want nil", r.SigInfo)
			}
			if tt.wantSigName != "" && (r.SigInfo == nil || r.SigInfo.SiSignoHuman != tt.wantSigName) {
				t.Errorf("SigInfo = %+v, want %s", r.SigInfo, tt.wantSigName)
			}

			if tt.wantThread == nil {
				if len(r.Error.Threads) != 0 || r.Error.Stack != nil || r.Error.ThreadName != "" {
					t.Errorf("threads=%d stack=%+v threadName=%q, want none", len(r.Error.Threads), r.Error.Stack, r.Error.ThreadName)
				}
				if !reportIncomplete(r) {
					t.Error("reportIncomplete = false, want true when there is no crashed stack")
				}
				return
			}
			if len(r.Error.Threads) != 1 {
				t.Fatalf("len(Threads) = %d, want 1", len(r.Error.Threads))
			}
			got := r.Error.Threads[0]
			if got.Crashed != tt.wantThread.Crashed || got.Name != tt.wantThread.Name || got.State != tt.wantThread.State ||
				got.Stack.Format != tt.wantThread.Stack.Format || got.Stack.Incomplete != tt.wantThread.Stack.Incomplete ||
				got.Stack.Frames != nil {
				t.Errorf("thread = %+v, want %+v with nil Frames", got, *tt.wantThread)
			}
			if r.Error.ThreadName != tt.wantThread.Name || r.Error.Stack == nil || !r.Error.Stack.Incomplete {
				t.Errorf("ThreadName = %q, Stack = %+v, want %q and an Incomplete stack", r.Error.ThreadName, r.Error.Stack, tt.wantThread.Name)
			}
		})
	}
}

func TestParseCrashDumpHeaderWithoutFramesBeforeNextHeader(t *testing.T) {
	r := parseCrashDump([]byte("panic: boom\n\ngoroutine 1 [running]:\ngoroutine 2 [chan receive]:\nmain.f()\n\t/a.go:1 +0x2\n"))

	if len(r.Error.Threads) != 2 {
		t.Fatalf("len(Threads) = %d, want 2", len(r.Error.Threads))
	}
	first, second := r.Error.Threads[0], r.Error.Threads[1]
	if first.Name != "goroutine 1" || len(first.Stack.Frames) != 0 || !first.Stack.Incomplete {
		t.Errorf("first = %q, %d frames, Incomplete=%t; want goroutine 1, 0 frames, Incomplete", first.Name, len(first.Stack.Frames), first.Stack.Incomplete)
	}
	if second.Name != "goroutine 2" || len(second.Stack.Frames) != 1 || second.Stack.Incomplete {
		t.Errorf("second = %q, %d frames, Incomplete=%t; want goroutine 2, 1 frame, complete", second.Name, len(second.Stack.Frames), second.Stack.Incomplete)
	}
}

// TestParseCrashDumpContinuesPastElisionMarker is the black-box version of
// TestParseFramesContinuesPastElisionMarker.
func TestParseCrashDumpContinuesPastElisionMarker(t *testing.T) {
	dump := []byte(`fatal error: stack overflow

goroutine 1 [running]:
main.recurse(0x0?)
	/tmp/main.go:10 +0x34
main.recurse(0x0?)
	/tmp/main.go:10 +0x34
...16777082 frames elided...
main.recurse(0x20?)
	/tmp/main.go:10 +0x34
main.main()
	/tmp/main.go:14 +0x24
runtime.main()
	/usr/local/go/src/runtime/proc.go:290 +0x2b4

goroutine 2 [chan receive]:
main.worker()
	/tmp/main.go:20 +0x1c
`)
	r := parseCrashDump(dump)

	if len(r.Error.Threads) != 2 {
		t.Fatalf("len(Threads) = %d, want 2 (the goroutine after the marker must still be found)", len(r.Error.Threads))
	}
	crashed := r.Error.Threads[0]
	if !crashed.Stack.Incomplete {
		t.Error("crashed Stack.Incomplete = false, want true: frames were elided")
	}
	// 2 frames before the marker and 3 after it. The marker is not a frame.
	if len(crashed.Stack.Frames) != 5 {
		t.Fatalf("len(frames) = %d, want 5", len(crashed.Stack.Frames))
	}
	if last := crashed.Stack.Frames[4]; last.Function != "runtime.main" {
		t.Errorf("last frame = %+v, want the resumed runtime.main frame", last)
	}
	for _, f := range crashed.Stack.Frames {
		if strings.Contains(f.Function, "elided") {
			t.Errorf("elision marker leaked into frames: %+v", f)
		}
	}
	if next := r.Error.Threads[1]; next.Stack.Incomplete || len(next.Stack.Frames) != 1 {
		t.Errorf("goroutine 2 = %+v, want one complete frame", next)
	}
}

// TestParseCrashDumpOversizedLine is the black-box version of
// TestSplitDumpHandlesOversizedLine.
func TestParseCrashDumpOversizedLine(t *testing.T) {
	dump := append([]byte("panic: boom\n"), bytes.Repeat([]byte("x"), maxCrashDumpSize+1)...)

	r := parseCrashDump(dump)

	if len(r.Error.Threads) != 1 {
		t.Fatalf("len(Threads) = %d, want 1", len(r.Error.Threads))
	}
	th := r.Error.Threads[0]
	if !th.Crashed || th.Name != "goroutine unknown" || th.State != "unknown" {
		t.Errorf("thread = %+v, want the synthesized crashed \"goroutine unknown\" thread", th)
	}
	if !th.Stack.Incomplete || len(th.Stack.Frames) != 0 {
		t.Errorf("thread Stack = %+v, want Incomplete with 0 frames", th.Stack)
	}
	if r.Error.Stack == nil || !r.Error.Stack.Incomplete || r.Error.ThreadName != "goroutine unknown" {
		t.Errorf("Error.Stack = %+v, ThreadName = %q, want an Incomplete stack for goroutine unknown", r.Error.Stack, r.Error.ThreadName)
	}
	if !reportIncomplete(r) {
		t.Error("reportIncomplete = false, want true")
	}
	// The line scanned before the oversized one survives as the preamble.
	if r.Error.Type != "panic" || r.Error.Message != "boom" {
		t.Errorf("Type = %q, Message = %q, want panic and boom", r.Error.Type, r.Error.Message)
	}
}
