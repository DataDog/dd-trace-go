// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package exportutil

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestSnippet(t *testing.T) {
	// Short bodies pass through, trimmed.
	assert.Equal(t, "boom", Snippet([]byte("  boom \n")))
	assert.Equal(t, "", Snippet(nil))
	assert.Equal(t, "", Snippet([]byte("   ")))

	// A body under the limit is returned whole.
	small := strings.Repeat("a", SnippetMaxBytes)
	assert.Equal(t, small, Snippet([]byte(small)))

	// An oversized ASCII body is truncated to the limit.
	big := strings.Repeat("a", SnippetMaxBytes+100)
	got := Snippet([]byte(big))
	assert.Len(t, got, SnippetMaxBytes)

	// An oversized body whose cut point lands mid-rune backs off to a rune
	// boundary so the snippet stays valid UTF-8 (never a partial multi-byte rune).
	// "é" is 2 bytes; fill so byte SnippetMaxBytes falls inside a rune.
	multibyte := strings.Repeat("é", SnippetMaxBytes) // 2*512 bytes
	got = Snippet([]byte(multibyte))
	assert.True(t, utf8.ValidString(got), "snippet must be valid UTF-8")
	assert.LessOrEqual(t, len(got), SnippetMaxBytes)
	assert.NotEmpty(t, got)

	// A short body with invalid UTF-8 bytes is sanitized (dropped), not returned raw.
	invalid := Snippet([]byte{'o', 'k', 0xff, 0xfe})
	assert.True(t, utf8.ValidString(invalid), "snippet must be valid UTF-8 even for garbage bytes")
	assert.Equal(t, "ok", invalid)
}

func TestAggregate(t *testing.T) {
	assert.NoError(t, Aggregate(0, 5, "llmobs/export"))
	err := Aggregate(2, 5, "llmobs/export")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "llmobs/export")
	assert.Contains(t, err.Error(), "2 of 5")
}
