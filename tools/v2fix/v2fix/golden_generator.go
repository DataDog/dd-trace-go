// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package v2fix

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// diff.Edit from golang.org/x/tools/internal/diff
// We define our own copy since the internal package is not accessible.
type diffEdit struct {
	Start, End int
	New        string
}

// applyEdits applies a sequence of edits to src and returns the result.
// Edits are applied in order of start position; edits with the same start
// position are applied in the order they appear in the slice.
func applyEdits(src []byte, edits []diffEdit) ([]byte, error) {
	if len(edits) == 0 {
		return src, nil
	}

	// Sort edits by start position
	slices.SortStableFunc(edits, func(a, b diffEdit) int {
		if a.Start != b.Start {
			return int(a.Start - b.Start)
		}
		return int(a.End - b.End)
	})

	var out bytes.Buffer
	pos := 0
	for _, edit := range edits {
		if edit.Start < pos {
			return nil, fmt.Errorf("overlapping edit at position %d (current pos: %d)", edit.Start, pos)
		}
		if edit.Start > len(src) || edit.End > len(src) {
			return nil, fmt.Errorf("edit out of bounds: start=%d end=%d len=%d", edit.Start, edit.End, len(src))
		}
		// Copy bytes before this edit
		out.Write(src[pos:edit.Start])
		// Write the new text
		out.WriteString(edit.New)
		pos = edit.End
	}
	// Copy remaining bytes
	out.Write(src[pos:])

	return out.Bytes(), nil
}

// runWithSuggestedFixesUpdate runs the analyzer and generates/updates golden files
// instead of comparing against them.
func runWithSuggestedFixesUpdate(t *testing.T, dir string, a *analysis.Analyzer, patterns ...string) {
	t.Helper()

	// Run the analyzer using the standard analysistest.Run
	results := analysistest.Run(t, dir, a, patterns...)

	// Process results to generate golden files
	for _, result := range results {
		act := result.Action

		// Collect edits per file and per message
		// file path -> message -> edits
		fileEdits := make(map[string]map[string][]diffEdit)
		fileContents := make(map[string][]byte)

		for _, diag := range act.Diagnostics {
			for _, fix := range diag.SuggestedFixes {
				for _, edit := range fix.TextEdits {
					start, end := edit.Pos, edit.End
					if !end.IsValid() {
						end = start
					}

					file := act.Package.Fset.File(start)
					if file == nil {
						continue
					}

					fileName := file.Name()
					if _, ok := fileContents[fileName]; !ok {
						contents, err := os.ReadFile(fileName)
						if err != nil {
							t.Errorf("error reading %s: %v", fileName, err)
							continue
						}
						fileContents[fileName] = contents
					}

					if _, ok := fileEdits[fileName]; !ok {
						fileEdits[fileName] = make(map[string][]diffEdit)
					}

					fileEdits[fileName][fix.Message] = append(
						fileEdits[fileName][fix.Message],
						diffEdit{
							Start: file.Offset(start),
							End:   file.Offset(end),
							New:   string(edit.NewText),
						},
					)
				}
			}
		}

		// Generate golden files
		for fileName, fixes := range fileEdits {
			orig := fileContents[fileName]
			goldenPath := fileName + ".golden"

			// Check if we have multiple different messages (requires txtar format)
			// or a single message (simpler format)
			messages := make([]string, 0, len(fixes))
			for msg := range fixes {
				messages = append(messages, msg)
			}
			sort.Strings(messages)

			var golden bytes.Buffer

			if len(messages) == 1 {
				// Single message: use simple txtar format with one section
				msg := messages[0]
				edits := fixes[msg]

				out, err := applyEdits(orig, edits)
				if err != nil {
					t.Errorf("error applying edits to %s: %v", fileName, err)
					continue
				}

				formatted, err := format.Source(out)
				if err != nil {
					// If formatting fails, use unformatted
					formatted = out
				}

				golden.WriteString("-- ")
				golden.WriteString(msg)
				golden.WriteString(" --\n")
				golden.Write(formatted)
			} else {
				// Multiple messages: create txtar archive with multiple sections
				for _, msg := range messages {
					edits := fixes[msg]

					out, err := applyEdits(orig, edits)
					if err != nil {
						t.Errorf("error applying edits to %s for message %q: %v", fileName, msg, err)
						continue
					}

					formatted, err := format.Source(out)
					if err != nil {
						formatted = out
					}

					golden.WriteString("-- ")
					golden.WriteString(msg)
					golden.WriteString(" --\n")
					golden.Write(formatted)
					golden.WriteString("\n")
				}
			}

			// Ensure the golden content ends with a newline
			content := golden.Bytes()
			if len(content) > 0 && content[len(content)-1] != '\n' {
				content = append(content, '\n')
			}

			if err := os.WriteFile(goldenPath, content, 0644); err != nil {
				t.Errorf("error writing golden file %s: %v", goldenPath, err)
				continue
			}

			// Get relative path for cleaner output
			relPath := goldenPath
			if rel, err := filepath.Rel(dir, goldenPath); err == nil {
				relPath = rel
			}
			t.Logf("Updated golden file: %s", relPath)
		}

		// Handle files that have diagnostics but no suggested fixes
		// (e.g., warnings without auto-fix)
		for _, diag := range act.Diagnostics {
			if len(diag.SuggestedFixes) == 0 {
				// Find the file for this diagnostic
				file := act.Package.Fset.File(diag.Pos)
				if file == nil {
					continue
				}

				fileName := file.Name()
				// Skip if we already processed this file with fixes
				if _, ok := fileEdits[fileName]; ok {
					continue
				}

				// Read file contents if not already cached
				if _, ok := fileContents[fileName]; !ok {
					contents, err := os.ReadFile(fileName)
					if err != nil {
						continue
					}
					fileContents[fileName] = contents
				}

				goldenPath := fileName + ".golden"

				// Check if golden file already exists - don't overwrite
				// if there's nothing to fix
				if _, err := os.Stat(goldenPath); err == nil {
					// Golden file exists, skip
					continue
				}

				// Create a golden file with just the message header and original content
				var golden bytes.Buffer
				golden.WriteString("-- ")
				golden.WriteString(diag.Message)
				golden.WriteString(" --\n")
				golden.Write(fileContents[fileName])

				content := golden.Bytes()
				if len(content) > 0 && content[len(content)-1] != '\n' {
					content = append(content, '\n')
				}

				if err := os.WriteFile(goldenPath, content, 0644); err != nil {
					t.Errorf("error writing golden file %s: %v", goldenPath, err)
					continue
				}

				relPath := goldenPath
				if rel, err := filepath.Rel(dir, goldenPath); err == nil {
					relPath = rel
				}
				t.Logf("Updated golden file (no fixes): %s", relPath)

				// Mark as processed
				fileEdits[fileName] = make(map[string][]diffEdit)
			}
		}
	}
}
