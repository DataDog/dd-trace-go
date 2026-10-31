// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"testing"
)

// canonicalJSON is a minimal, already-canonical supported_configurations.json
// file: two-space indented, no HTML escaping, and no trailing newline (the
// exact format addSupportedConfigurationsKeys writes via generate/add).
const canonicalJSON = `{
  "version": "2",
  "supportedConfigurations": {
    "DD_SERVICE": [
      {
        "implementation": "A",
        "type": "String",
        "default": null
      }
    ]
  }
}`

func parseConfig(t *testing.T, raw string) *supportedConfiguration {
	t.Helper()
	var cfg supportedConfiguration
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("failed to parse fixture JSON: %v", err)
	}
	return &cfg
}

// TestIsCanonicalFormatAcceptsCanonicalFile ensures isCanonicalFormat does not
// flag a file that is already in the exact canonical form (no trailing
// newline, two-space indent). This is a control case: it must pass so the
// stricter trailing-whitespace check below does not produce false positives
// on well-formed input.
func TestIsCanonicalFormatAcceptsCanonicalFile(t *testing.T) {
	cfg := parseConfig(t, canonicalJSON)
	ok, err := isCanonicalFormat([]byte(canonicalJSON), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected canonical file to be reported as canonical")
	}
}

// TestIsCanonicalFormatRejectsTrailingNewline is a regression test for a
// reviewer-caught defect: a hand edit (for example, a manual edit of
// supported_configurations.json, or a tool that doesn't trim its output the
// way addSupportedConfigurationsKeys does) can leave a trailing newline at the
// end of the file. The JSON content is otherwise byte-identical to the
// canonical form, so a naive comparison that trims trailing newlines from
// both sides before comparing would silently accept the drift.
// isCanonicalFormat must instead compare against the exact canonical form and
// reject this.
func TestIsCanonicalFormatRejectsTrailingNewline(t *testing.T) {
	raw := canonicalJSON + "\n"
	cfg := parseConfig(t, raw)
	ok, err := isCanonicalFormat([]byte(raw), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected a file with an added trailing newline to be rejected")
	}
}

// TestIsCanonicalFormatRejectsTrailingWhitespace covers the same class of
// drift as TestIsCanonicalFormatRejectsTrailingNewline, but for trailing
// whitespace instead of a bare newline.
func TestIsCanonicalFormatRejectsTrailingWhitespace(t *testing.T) {
	raw := canonicalJSON + "   "
	cfg := parseConfig(t, raw)
	ok, err := isCanonicalFormat([]byte(raw), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected a file with trailing whitespace to be rejected")
	}
}

// TestIsCanonicalFormatAcceptsCRLF ensures the CRLF-to-LF normalization for
// Windows checkouts (git core.autocrlf) still passes for an otherwise
// canonical file, so this stricter check does not regress that existing
// behavior.
func TestIsCanonicalFormatAcceptsCRLF(t *testing.T) {
	crlf := ""
	for _, line := range splitLines(canonicalJSON) {
		crlf += line + "\r\n"
	}
	// Canonical form has no trailing newline; drop the one just added.
	crlf = crlf[:len(crlf)-2]
	cfg := parseConfig(t, canonicalJSON)
	ok, err := isCanonicalFormat([]byte(crlf), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a CRLF-normalized canonical file to be accepted")
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
