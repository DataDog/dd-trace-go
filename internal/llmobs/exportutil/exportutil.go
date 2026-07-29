// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package exportutil contains shared export helpers.
package exportutil

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const SnippetMaxBytes = 512

// Snippet returns a bounded, valid UTF-8 response excerpt.
func Snippet(b []byte) string {
	s := strings.ToValidUTF8(strings.TrimSpace(string(b)), "")
	if len(s) <= SnippetMaxBytes {
		return s
	}
	cut := SnippetMaxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Aggregate summarizes request failures.
func Aggregate(failed, total int, prefix string) error {
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%s: %d of %d request(s) failed", prefix, failed, total)
}
