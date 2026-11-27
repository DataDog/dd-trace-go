// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package instrumentation

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTruncateUTF8(t *testing.T) {
	const prefix = "ab"
	for _, r := range []string{
		"é", // U+00E9, 2 bytes
		"€", // U+20AC, 3 bytes
		"😀", // U+1F600, 4 bytes
	} {
		// k is the number of bytes of r that fit under the limit.
		for k := 0; k <= len(r); k++ {
			want := prefix
			if k == len(r) {
				want += r
			}
			limit := len(prefix) + k
			t.Run(strconv.Itoa(len(r))+"-byte/"+strconv.Itoa(k), func(t *testing.T) {
				assert.Equal(t, want, TruncateUTF8(prefix+r+"cd", limit))
				// The same input already cut to limit bytes, as a buffered peek returns it.
				assert.Equal(t, want, TruncateUTF8((prefix + r + "cd")[:limit], limit))
			})
		}
	}

	for _, tt := range []struct {
		name  string
		s     string
		limit int
		want  string
	}{
		{name: "ascii", s: "ABCDEFG", limit: 4, want: "ABCD"},
		{name: "ascii exact", s: "ABCD", limit: 4, want: "ABCD"},
		{name: "shorter than limit", s: "ab😀", limit: 100, want: "ab😀"},
		{name: "exact boundary", s: "ab😀", limit: 6, want: "ab😀"},
		{name: "empty", s: "", limit: 4, want: ""},
		{name: "zero limit", s: "abc", limit: 0, want: ""},
		{name: "negative limit", s: "abc", limit: -1, want: ""},
		{name: "only a cut rune", s: "😀", limit: 3, want: ""},
		// Malformed bytes are left as they were; only a rune cut by the limit is dropped.
		{name: "invalid lead bytes", s: "ab\xff\xfe\xfd\xfccd", limit: 6, want: "ab\xff\xfe\xfd\xfc"},
		{name: "invalid continuation bytes", s: "ab\x80\x80\x80\x80cd", limit: 6, want: "ab\x80\x80\x80\x80"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TruncateUTF8(tt.s, tt.limit))
		})
	}

	t.Run("every limit", func(t *testing.T) {
		s := strings.Repeat("aé€😀", 8)
		for limit := 0; limit <= len(s)+1; limit++ {
			got := TruncateUTF8(s, limit)
			assert.True(t, utf8.ValidString(got), "limit %d", limit)
			assert.True(t, strings.HasPrefix(s, got), "limit %d", limit)
			assert.LessOrEqual(t, len(got), limit, "limit %d", limit)
			// At most the bytes of one cut rune are dropped.
			assert.Greater(t, len(got), min(limit, len(s))-utf8.UTFMax, "limit %d", limit)
		}
	})
}

func BenchmarkTruncateUTF8(b *testing.B) {
	for _, bc := range []struct {
		name string
		s    string
	}{
		{"ascii", strings.Repeat("a", 10*1024)},
		{"multibyte", strings.Repeat("€", 10*1024)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = TruncateUTF8(bc.s, 5*1024)
			}
		})
	}
}
