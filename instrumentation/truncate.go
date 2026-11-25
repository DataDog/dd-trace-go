// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package instrumentation

import "unicode/utf8"

// TruncateUTF8 returns s cut to at most limit bytes without splitting a UTF-8
// encoded rune: a rune that the cut would split is dropped entirely, so valid
// UTF-8 stays valid. A negative limit is treated as zero.
//
// A string of exactly limit bytes may itself be the result of an earlier cut,
// such as a buffered peek at a request body, so a rune left incomplete at its
// end is dropped too. Only the last utf8.UTFMax bytes are inspected, and
// malformed UTF-8 is otherwise returned unchanged.
//
// Use it when a span tag value is cut to a byte limit.
func TruncateUTF8(s string, limit int) string {
	if len(s) < limit {
		return s
	}
	s = s[:max(limit, 0)]
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			if !utf8.FullRuneInString(s[i:]) {
				return s[:i]
			}
			break
		}
	}
	return s
}
