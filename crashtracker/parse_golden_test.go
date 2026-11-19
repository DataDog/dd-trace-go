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
// before comparison; DDTags and TraceID are not set by parseCrashDump.
//
// Fixture provenance:
//
//   - real_*.txt are unedited captures of the stderr of small Go programs
//     built with go1.27.4 on darwin/arm64 (see TestParseCrashDumpFixtures for
//     the per-fixture expectations). Only the program source path was chosen
//     to be /tmp/crashdemo/<name>/main.go.
//   - synthetic_*.txt are hand-edited from real captures to reach runtime
//     output shapes that are hard to produce on demand. What each one
//     simulates is documented next to its expectations in
//     TestParseCrashDumpFixtures.
//   - The older fixtures without a prefix were added with the parser.
func TestParseCrashDumpGolden(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no testdata/*.txt fixtures found")
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
