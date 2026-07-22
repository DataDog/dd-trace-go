// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package exportutil holds small helpers shared by the offline export clients
// (llmobs/export and otlp/export): bounded response-body snippets and
// per-request failure aggregation.
package exportutil

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SnippetMaxBytes bounds the length of a diagnostic response-body snippet.
const SnippetMaxBytes = 512

// Snippet trims b and truncates it to a bounded, UTF-8-safe diagnostic excerpt.
func Snippet(b []byte) string {
	// Drop invalid UTF-8 up front so a body with control/garbage bytes (and any
	// body at or below the limit) still yields a valid-UTF-8 snippet.
	s := strings.ToValidUTF8(strings.TrimSpace(string(b)), "")
	if len(s) <= SnippetMaxBytes {
		return s
	}
	// Back off to a rune boundary so the snippet stays valid UTF-8.
	cut := SnippetMaxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Aggregate rolls per-request failures into a single summary error, or nil when
// none failed. prefix identifies the calling package (e.g. "llmobs/export").
func Aggregate(failed, total int, prefix string) error {
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%s: %d of %d request(s) failed", prefix, failed, total)
}
