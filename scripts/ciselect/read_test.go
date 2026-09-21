// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readText reads a repository file and normalises line endings.
//
// Several tests in this package match YAML with line-anchored regexes. Windows
// checkouts carry CRLF (only supported_configurations.json is pinned to LF in
// .gitattributes), and a literal \n in a pattern does not match \r\n -- so
// those tests silently found nothing and failed on windows-latest while
// passing everywhere else. Normalise once, here, rather than making every
// pattern CRLF-aware.
func readText(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

// TestFilePatternsSurviveCRLF is the regression guard. It writes a CRLF copy
// of each parsed file to a temporary directory, reads it back through readText
// -- the same path the real tests use -- and asserts the patterns still match.
// That makes the Windows failure reproducible on any platform.
func TestFilePatternsSurviveCRLF(t *testing.T) {
	_, _, root := testTable(t)

	tests := []struct {
		name string
		rel  string
		find func(string) int
	}{
		{"action fallback gate list", actionPath, func(s string) int {
			return len(fallbackGate.FindAllStringSubmatch(s, -1))
		}},
		{"action output declarations", actionPath, func(s string) int {
			return len(actionOutput.FindAllStringSubmatch(s, -1))
		}},
		{"gitlab BENCHMARKS variables", microBenchPath, func(s string) int {
			return len(benchmarksVar.FindAllStringSubmatch(s, -1))
		}},
		{"gitlab benchmark-changes block", microBenchPath, func(s string) int {
			return len(changesPaths.FindAllStringSubmatch(s, -1))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.find(readText(t, root, tt.rel))
			if want == 0 {
				t.Fatalf("%s: pattern matches nothing even with LF endings", tt.rel)
			}

			// A Windows checkout, byte for byte.
			dir := t.TempDir()
			name := filepath.Base(tt.rel)
			raw, err := os.ReadFile(filepath.Join(root, tt.rel))
			if err != nil {
				t.Fatalf("read %s: %v", tt.rel, err)
			}
			crlf := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n", "\r\n")
			if err := os.WriteFile(filepath.Join(dir, name), []byte(crlf), 0o600); err != nil {
				t.Fatalf("write CRLF copy: %v", err)
			}

			if got := tt.find(readText(t, dir, name)); got != want {
				t.Errorf("%s: %d matches from a CRLF checkout, %d from LF. Every file this "+
					"package parses with a line-anchored regex must be read through readText.",
					tt.rel, got, want)
			}
		})
	}
}

// TestReadTextNormalises pins the helper itself, so the guard above cannot be
// defeated by readText quietly losing its normalisation.
func TestReadTextNormalises(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.yml"), []byte("a: 1\r\nb: 2\r\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := readText(t, dir, "f.yml"), "a: 1\nb: 2\n"; got != want {
		t.Errorf("readText() = %q, want %q", got, want)
	}
}
