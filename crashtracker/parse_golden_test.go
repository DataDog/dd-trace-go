// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden rewrites testdata/<fixture>.golden.json from the current
// parseCrashDump output. Run:
//
//	go test ./crashtracker -run TestParseCrashDumpGolden -update
//
// and review the resulting diff before committing it.
var updateGolden = flag.Bool("update", false, "rewrite the crashtracker golden files in testdata")

// TestParseCrashDumpGolden pins the end-to-end output of parseCrashDump for
// every testdata/*.txt fixture. It characterizes the parser as it is, bugs
// and gaps included, so that any change to the parser shows up as an explicit
// golden diff. Fields that vary between runs (Timestamp, OSInfo) are zeroed
// before comparison; DDTags and TraceID are not set by parseCrashDump. A
// golden file without a matching .txt fixture fails the test.
//
// Fixture provenance. The fixtures without a prefix were added with the
// parser. The real_* fixtures are unedited stderr of small programs built with
// go1.27.4 on darwin/arm64 and run as a built binary (not "go run"). Their
// sources were /tmp/crashdemo/<name>/main.go.
//
//	real_panic_traceback_system: GOTRACEBACK=system, nil pointer dereference
//	  with blocked goroutines. Extended headers ("gp=... m=... mp=...") and
//	  "fp=... sp=... pc=..." location suffixes.
//	real_sigabrt_traceback_crash: GOTRACEBACK=crash, the program sends SIGABRT
//	  to itself. "goroutine 0" blocks, SIGQUIT blocks and register dumps.
//	real_tracebackancestors: GOTRACEBACK=all GODEBUG=tracebackancestors=5,
//	  nil map write. "[originating from goroutine N]:" blocks.
//	real_created_by: GOTRACEBACK=all, index out of range. "created by X in
//	  goroutine N" lines.
//	real_goroutine_labels: GOTRACEBACK=all GODEBUG=tracebacklabels=1, nil map
//	  write. "goroutine N [state] {key: value}:" headers.
//
// The synthetic_* fixtures are hand-edited from the real ones to reach shapes
// that are hard to produce on demand:
//
//	synthetic_state_suffixes: header suffixes from goroutineheader in
//	  runtime/traceback.go (go1.27.4): minutes, "locked to thread", "leaked",
//	  "durable" and "synctest bubble N".
//	synthetic_colon_digit_path: a location whose directory name has ":"
//	  followed by digits, as in /tmp/dir:123/main.go:2.
//	synthetic_frames_elided: the "...N frames elided..." marker of a stack
//	  overflow, with a leading "runtime stack:" block and a second goroutine
//	  after the elided stack.
//	synthetic_windows_paths: a Windows panic with "C:/..." paths, a path with
//	  a space and a "[signal 0xc0000005 ...]" line.
//	synthetic_truncated_function_line: dump cut after a function line, with no
//	  trailing newline.
//	synthetic_truncated_location_line: dump cut inside a location line, with no
//	  trailing newline.
//
// KNOWN GAPs. The goldens pin this behavior as it is today. A fix changes the
// golden of the fixture named here.
//
//   - sigsegv, real_panic_traceback_system: for a signal crash Error.Message
//     is the "[signal ...]" line, so the panic text is not in it.
//   - real_sigabrt_traceback_crash: no goroutine is "running", so the crashed
//     thread is the first one, "goroutine 0 [idle]", not the user goroutine
//     that called kill. Five threads share the name "goroutine 0". The
//     register dump that directly follows the last frame of four of those
//     blocks is read as a function line without a location, so those four
//     threads are marked Incomplete although all their frames were parsed.
//   - real_tracebackancestors: "[originating from goroutine N]:" blocks are
//     not understood. The rest of that goroutine's block is dropped, and the
//     three goroutines that have ancestors are marked Incomplete. The crashed
//     goroutine keeps only the frames before its "created by" line.
//   - real_goroutine_labels: the header ends with "] {key: value}:", which
//     goroutineHeaderRe does not match. The labeled goroutine is dropped, with
//     no Incomplete flag.
//   - synthetic_state_suffixes: crashingThread compares State with "running"
//     exactly. The crashing goroutine is "running, locked to thread", so it is
//     not recognized and the first goroutine in the dump is reported as the
//     crashed one.
//   - synthetic_windows_paths: SigInfo stays nil, because the line is
//     "[signal 0xc0000005 ...]" and not "[signal SIGSEGV ...]". The line
//     becomes the second line of Error.Message.
//   - synthetic_truncated_location_line: the cut location line still starts
//     with a tab, so it becomes a frame with a truncated file and line 0. The
//     stack is not marked Incomplete.
func TestParseCrashDumpGolden(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no testdata/*.txt fixtures found")
	}

	goldens, err := filepath.Glob(filepath.Join("testdata", "*.golden.json"))
	if err != nil {
		t.Fatalf("glob goldens: %v", err)
	}
	for _, golden := range goldens {
		fixture := strings.TrimSuffix(golden, ".golden.json") + ".txt"
		if _, err := os.Stat(fixture); err != nil {
			t.Errorf("golden file %s has no matching fixture %s: %v", golden, fixture, err)
		}
	}

	for _, fixture := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixture), ".txt")
		t.Run(name, func(t *testing.T) {
			dump, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			got := marshalGolden(t, parseCrashDump(dump))
			goldenPath := filepath.Join("testdata", name+".golden.json")

			if *updateGolden {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			// A Windows checkout may convert the golden file to CRLF.
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(got, want) {
				t.Errorf("parseCrashDump output differs from %s (run with -update to accept):\n%s",
					goldenPath, firstDifference(string(want), string(got)))
			}
		})
	}
}

// marshalGolden renders a Report as indented JSON after zeroing the fields
// that differ between runs and platforms.
func marshalGolden(t *testing.T, r *Report) []byte {
	t.Helper()
	if r == nil {
		t.Fatal("parseCrashDump returned nil")
	}
	normalized := *r
	normalized.Timestamp = 0
	normalized.OSInfo = OSInfo{}
	normalized.DDTags = ""
	normalized.TraceID = ""
	out, err := json.MarshalIndent(&normalized, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return append(out, '\n')
}

// firstDifference describes the first line at which want and got differ, with
// a few lines of context from each side.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	n := min(len(wantLines), len(gotLines))
	idx := n
	for i := range n {
		if wantLines[i] != gotLines[i] {
			idx = i
			break
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d (golden has %d lines, got %d lines)\n", idx+1, len(wantLines), len(gotLines))
	lo := max(idx-3, 0)
	for i := lo; i < idx; i++ {
		fmt.Fprintf(&b, "  %5d   %s\n", i+1, wantLines[i])
	}
	for i := idx; i < min(idx+3, len(wantLines)); i++ {
		fmt.Fprintf(&b, "  %5d - %s\n", i+1, wantLines[i])
	}
	for i := idx; i < min(idx+3, len(gotLines)); i++ {
		fmt.Fprintf(&b, "  %5d + %s\n", i+1, gotLines[i])
	}
	return b.String()
}
