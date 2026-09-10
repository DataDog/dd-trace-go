// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// oldSanitize is the reference implementation used as a correctness oracle.
// It reproduces the original strings.Map-based stringMutator.Mutate logic.
func oldSanitize(fn func(rune) (rune, bool), s string) string {
	inRun := false
	return strings.Map(func(r rune) rune {
		v, collapse := fn(r)
		if v < 0 {
			inRun = false
			return -1
		}
		if collapse {
			if !inRun {
				inRun = true
				return v
			}
			return -1
		}
		inRun = false
		return v
	}, s)
}

// Reference fn implementations mirror the originals exactly.
var (
	refKeyFn = func(r rune) (rune, bool) {
		switch {
		case r == ',' || r == '=':
			return '_', false
		case r < 0x20 || r > 0x7E:
			return '_', true
		}
		return r, false
	}
	refValueFn = func(r rune) (rune, bool) {
		switch {
		case r == '=':
			return '~', false
		case r == ',' || r == '~' || r == ';':
			return '_', false
		case r < 0x20 || r > 0x7E:
			return '_', true
		}
		return r, false
	}
	refOriginFn = func(r rune) (rune, bool) {
		switch {
		case r == '=':
			return '~', false
		case r == ',' || r == '~' || r == ';':
			return '_', false
		case r < 0x21 || r > 0x7E:
			return '_', true
		}
		return r, false
	}
)

// --- Correctness oracle tests ---

var sanitizeKeyTable = []struct {
	name  string
	input string
}{
	{"empty", ""},
	{"clean ascii", "usr.id"},
	{"clean with dots", "_dd.p.usr.email"},
	{"comma", "a,b"},
	{"equals", "a=b"},
	{"both", "a,b=c"},
	{"control chars", "a\x01\x02b"},
	{"collapse ctrl", "\x01\x02\x03"},
	{"0x7F", "a\x7Fb"},
	{"space is passthrough", "a b"},
	{"tilde passthrough for key", "a~b"},
	{"non-ascii single", "café"},
	{"non-ascii run", "中文"},
	{"mixed", "a,bé=c\x01\x02d"},
}

func TestSanitizeTagKeyOracle(t *testing.T) {
	for _, tt := range sanitizeKeyTable {
		t.Run(tt.name, func(t *testing.T) {
			want := oldSanitize(refKeyFn, tt.input)
			got := sanitizeTagKey(tt.input)
			assert.Equal(t, want, got)
		})
	}
}

var sanitizeValueTable = []struct {
	name  string
	input string
}{
	{"empty", ""},
	{"clean ascii", "somevalue"},
	{"equals encoded as tilde", "a=b"},
	{"tilde replaced", "a~b"},
	{"comma replaced", "a,b"},
	{"semicolon replaced", "a;b"},
	{"multiple specials", "a,b;c~d=e"},
	{"control chars collapse", "\x01\x02\x03"},
	{"0x7F", "val\x7F"},
	{"space passthrough", "a b"},
	{"non-ascii run", "中文 text"},
	{"all disallowed", ",;~="},
}

func TestSanitizeTagValueOracle(t *testing.T) {
	for _, tt := range sanitizeValueTable {
		t.Run(tt.name, func(t *testing.T) {
			want := oldSanitize(refValueFn, tt.input)
			got := sanitizeTagValue(tt.input)
			assert.Equal(t, want, got)
		})
	}
}

var sanitizeOriginTable = []struct {
	name  string
	input string
}{
	{"empty", ""},
	{"clean ascii", "synthetics"},
	{"space collapses", "a b c"},
	{"multiple spaces collapse", "a   b"},
	{"equals encoded", "a=b"},
	{"tilde replaced", "a~b"},
	{"comma replaced", "a,b"},
	{"semicolon replaced", "a;b"},
	{"control then space", "\x01 b"},
	{"non-ascii collapse", "中文"},
	{"all disallowed", " \x01,;~="},
	{"mixed realistic", "synthetics_browser"},
}

func TestSanitizeOriginOracle(t *testing.T) {
	for _, tt := range sanitizeOriginTable {
		t.Run(tt.name, func(t *testing.T) {
			want := oldSanitize(refOriginFn, tt.input)
			got := sanitizeOrigin(tt.input)
			assert.Equal(t, want, got)
		})
	}
}

// TestSanitizeZeroAllocNoMatch verifies that clean inputs return the original
// string pointer (no allocation, no copy).
func TestSanitizeZeroAllocNoMatch(t *testing.T) {
	clean := "usr.email"
	assert.True(t, sanitizeTagKey(clean) == clean, "expected same string pointer (no alloc)")
	assert.True(t, sanitizeTagValue(clean) == clean, "expected same string pointer (no alloc)")
	assert.True(t, sanitizeOrigin("synthetics") == "synthetics", "expected same string pointer (no alloc)")
}

// --- Fuzz tests ---

func FuzzSanitizeTagKey(f *testing.F) {
	for _, tt := range sanitizeKeyTable {
		f.Add(tt.input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip()
		}
		want := oldSanitize(refKeyFn, input)
		got := sanitizeTagKey(input)
		if want != got {
			t.Fatalf("sanitizeTagKey(%q): want %q got %q", input, want, got)
		}
	})
}

func FuzzSanitizeTagValue(f *testing.F) {
	for _, tt := range sanitizeValueTable {
		f.Add(tt.input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip()
		}
		want := oldSanitize(refValueFn, input)
		got := sanitizeTagValue(input)
		if want != got {
			t.Fatalf("sanitizeTagValue(%q): want %q got %q", input, want, got)
		}
	})
}

// FuzzSanitizeTagKeyAlt cross-checks sanitizeTagKey against the reference regexp
// (the same guarantee that PR 4805's FuzzDefaultObfuscator provides).
func FuzzSanitizeTagKeyAlt(f *testing.F) {
	rx := regexp.MustCompile(`,|=|[^\x20-\x7E]+`)
	f.Add("usr.id")
	f.Add("a,b=c\x01")
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip()
		}
		want := rx.ReplaceAllString(input, "_")
		got := sanitizeTagKey(input)
		if want != got {
			t.Fatalf("sanitizeTagKey(%q): regexp=%q lut=%q", input, want, got)
		}
	})
}

// FuzzSanitizeTagValueAlt cross-checks sanitizeTagValue against its reference regexp.
func FuzzSanitizeTagValueAlt(f *testing.F) {
	rxReplace := regexp.MustCompile(`,|;|~|[^\x20-\x7E]+`)
	f.Add("somevalue")
	f.Add("a=b,c;d~e")
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip()
		}
		// Reference: collapse/replace disallowed chars, then encode '=' as '~'.
		want := strings.ReplaceAll(rxReplace.ReplaceAllString(input, "_"), "=", "~")
		got := sanitizeTagValue(input)
		if want != got {
			t.Fatalf("sanitizeTagValue(%q): regexp=%q lut=%q", input, want, got)
		}
	})
}

// --- Benchmarks ---

// BenchmarkSanitizeTagKey covers three input shapes:
//   - no-match: all passthrough (exercises zero-allocation path)
//   - all-disallowed: every byte triggers a replace or collapse
//   - mixed: typical production tag key
func BenchmarkSanitizeTagKey(b *testing.B) {
	cases := []struct {
		name  string
		input string
	}{
		{"no_match_short", "usr.id"},
		{"no_match_long", strings.Repeat("usr.id.", 20)},
		{"all_disallowed", strings.Repeat(",=", 40)},
		{"mixed_realistic", "_dd.p.usr.email.address"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = sanitizeTagKey(tc.input)
			}
		})
	}
}

// BenchmarkSanitizeTagValue covers the same three shapes for tag values.
func BenchmarkSanitizeTagValue(b *testing.B) {
	cases := []struct {
		name  string
		input string
	}{
		{"no_match_short", "somevalue"},
		{"no_match_long", strings.Repeat("somevalue.", 20)},
		{"all_disallowed", strings.Repeat(",;~=", 30)},
		{"mixed_realistic", "usr_id_12345"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = sanitizeTagValue(tc.input)
			}
		})
	}
}

// BenchmarkSanitizeOrigin covers origin field shapes.
func BenchmarkSanitizeOrigin(b *testing.B) {
	cases := []struct {
		name  string
		input string
	}{
		{"no_match", "synthetics"},
		{"with_spaces", "synthetics browser"},
		{"all_disallowed", strings.Repeat(" ,;~=", 20)},
		{"mixed_realistic", "synthetics_browser_ci"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = sanitizeOrigin(tc.input)
			}
		})
	}
}
