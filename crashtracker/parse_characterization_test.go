// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// The tests in this file characterize the current behavior of parseCrashDump
// through its public result only. They deliberately avoid splitDump and the
// other parser internals so that they keep working when the
// goroutine and frame parser is replaced. They cover what the golden files in
// testdata cannot express: caps, degenerate input and oversized input. Where
// the behavior looks like a bug or a gap, the test still pins it and the case
// carries a "KNOWN GAP" comment. The per-fixture facts and gaps are in the
// comment of TestParseCrashDumpGolden.

// TestReportIncompleteFollowsCrashedStack checks that only the crashed
// goroutine's stack decides whether the report is incomplete. The goldens do
// not show this: they pin the parsed report, not the derived tag.
func TestReportIncompleteFollowsCrashedStack(t *testing.T) {
	complete := "goroutine 1 [running]:\nmain.crash()\n\t/app/crash.go:10 +0x1\n"
	cut := "goroutine 2 [sleep]:\nmain.sleeper()\n"
	if r := parseCrashDump([]byte("panic: boom\n\n" + complete + "\n" + cut)); reportIncomplete(r) {
		t.Error("crashed stack complete, other stack cut: reportIncomplete = true, want false")
	}
	if r := parseCrashDump([]byte("panic: boom\n\ngoroutine 1 [running]:\nmain.crash()\n")); !reportIncomplete(r) {
		t.Error("crashed stack cut: reportIncomplete = false, want true")
	}
}

// goroutineDump builds a dump with a "panic: boom" preamble and the given
// goroutine blocks.
func goroutineDump(blocks ...string) []byte {
	return []byte("panic: boom\n\n" + strings.Join(blocks, "\n"))
}

// goroutineBlock builds one goroutine block with n frames. Frame i (counting
// from the innermost, from 0) is at line i+1 of /tmp/main.go, so a test can
// tell which frames a cap kept.
func goroutineBlock(id int, state string, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "goroutine %d [%s]:\n", id, state)
	for i := range n {
		fmt.Fprintf(&b, "main.recurse(0x1)\n\t/tmp/main.go:%d +0x20\n", i+1)
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
			// The cap keeps the innermost frames: lines 1..wantFrames, in order.
			for i, f := range deep.Stack.Frames {
				if want := (Frame{Function: "main.recurse", File: "/tmp/main.go", Line: i + 1}); f != want {
					t.Fatalf("frame %d = %+v, want %+v", i, f, want)
				}
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
	defer log.UseLogger(log.DiscardLogger{})() // capThreads warns on every truncation
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

	// KNOWN GAP: when a goroutine header follows any line of the previous
	// block without a blank line (another header, a frame or a "created by"
	// line), gostackparse consumes it while it aborts the previous goroutine,
	// so that goroutine is lost. The runtime prints "\n" before each
	// goroutine header (tracebacksomeothers in runtime/traceback.go), so this
	// cannot come from runtime output.
	if len(r.Error.Threads) != 1 {
		t.Fatalf("len(Threads) = %d, want 1", len(r.Error.Threads))
	}
	first := r.Error.Threads[0]
	if first.Name != "goroutine 1" || len(first.Stack.Frames) != 0 || !first.Stack.Incomplete {
		t.Errorf("first = %q, %d frames, Incomplete=%t; want goroutine 1, 0 frames, Incomplete", first.Name, len(first.Stack.Frames), first.Stack.Incomplete)
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

// TestParseCrashDumpOversizedLineEndingInCarriageReturn checks that a line of
// exactly maxCrashDumpSize bytes counts as oversized even though its trailing
// "\r" is not part of the text.
func TestParseCrashDumpOversizedLineEndingInCarriageReturn(t *testing.T) {
	dump := append(bytes.Repeat([]byte("x"), maxCrashDumpSize-1), '\r')

	r := parseCrashDump(dump)

	if len(r.Error.Threads) != 1 {
		t.Fatalf("len(Threads) = %d, want 1", len(r.Error.Threads))
	}
	th := r.Error.Threads[0]
	if !th.Crashed || th.Name != "goroutine unknown" || !th.Stack.Incomplete || len(th.Stack.Frames) != 0 {
		t.Errorf("thread = %+v, want the synthesized crashed Incomplete \"goroutine unknown\" thread", th)
	}
}

// TestParseCrashDumpLongLocationLine checks that a location line longer than
// the default 64 KiB line limit of the parser does not hide the crash frame
// or the goroutines after it. The dump has the shape of a real crash: it comes
// from a program with a "//line" directive that sets a 70000-byte file name,
// run with GOTRACEBACK=all. The dump here is synthetic to keep the test fast.
func TestParseCrashDumpLongLocationLine(t *testing.T) {
	longFile := "/tmp/" + strings.Repeat("a", 70000) + ".go"
	dump := []byte("panic: boom\n\n" +
		"goroutine 1 [running]:\nmain.main()\n\t" + longFile + ":10 +0x1c\n\n" +
		"goroutine 5 [sleep]:\nmain.sleeper()\n\t/tmp/main.go:6 +0x2\n")

	r := parseCrashDump(dump)

	if len(r.Error.Threads) != 2 {
		t.Fatalf("len(Threads) = %d, want 2", len(r.Error.Threads))
	}
	crashed, later := r.Error.Threads[0], r.Error.Threads[1]
	if !crashed.Crashed || crashed.Name != "goroutine 1" || crashed.Stack.Incomplete || len(crashed.Stack.Frames) != 1 {
		t.Fatalf("crashed = %q (crashed=%t, incomplete=%t, %d frames), want goroutine 1, complete, 1 frame",
			crashed.Name, crashed.Crashed, crashed.Stack.Incomplete, len(crashed.Stack.Frames))
	}
	if got, want := crashed.Stack.Frames[0], (Frame{Function: "main.main", File: longFile, Line: 10}); got != want {
		t.Errorf("crash frame = {%s %d bytes %d}, want {%s %d bytes %d}", got.Function, len(got.File), got.Line, want.Function, len(want.File), want.Line)
	}
	if later.Name != "goroutine 5" || later.Stack.Incomplete || len(later.Stack.Frames) != 1 {
		t.Errorf("later = %q (incomplete=%t, %d frames), want goroutine 5, complete, 1 frame", later.Name, later.Stack.Incomplete, len(later.Stack.Frames))
	}
}
