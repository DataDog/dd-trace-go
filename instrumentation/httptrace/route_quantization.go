// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package httptrace

import (
	"strings"
)

// QuantizeURL quantizes a URL path into a more generic form that resembles a route.
func QuantizeURL(path string) string {
	var quantizer urlQuantizer
	return quantizer.Quantize(path)
}

// urlQuantizer is responsible for quantizing URLs paths into a more generic form that resembles a route
// in case a handler pattern is not available. net/http was the last framework where we did not have access to it
// until go 1.22. Now this algorithm is only used in proxy implementations where handlers don't make sense.
type urlQuantizer struct {
	tokenizer tokenizer
	buf       strings.Builder
}

// Quantize path (eg /segment1/segment2/segment3) by doing the following:
// * If a segment contains only letters, we keep it as it is;
// * If a segment contains one or more digits or special characters, we replace it by '*'
// * If a segments represents an API version (eg. v123) we keep it as it is
func (q *urlQuantizer) Quantize(path string) string {
	if len(path) == 0 {
		return ""
	}

	if path[0] != '/' {
		path = "/" + path
	}

	q.tokenizer.Reset(path)
	q.buf.Reset()
	replacements := 0

	for q.tokenizer.Next() {
		q.buf.WriteByte('/')
		tokenType, tokenValue := q.tokenizer.Value()
		if tokenType == tokenWildcard {
			replacements++
			q.buf.WriteByte('*')
			continue
		}

		q.buf.WriteString(tokenValue)
	}

	if replacements == 0 {
		return path
	}

	// Copy quantized path into original byte slice
	return q.buf.String()
}

// tokenType represents a type of token handled by the `tokenizer`
type tokenType string

const (
	// tokenUnknown represents a token of type unknown
	tokenUnknown = "token:unknown"
	// tokenWildcard represents a token that contains digits or special chars
	tokenWildcard = "token:wildcard"
	// tokenString represents a token that contains only letters
	tokenString = "token:string"
	// tokenAPIVersion represents an API version (eg. v123)
	tokenAPIVersion = "token:api-version"
)

// tokenizer provides a stream of tokens for a given URL
type tokenizer struct {
	// These variables represent the moving cursors (left and right side
	// respectively) of the tokenizer. After each "Next()" execution, they will
	// point to the beginning and end of a segment like the following:
	//
	// /segment1/segment2/segment3
	// ----------^-------^--------
	//           i       j
	//
	i, j int

	path string

	countAllowedChars int // a-Z, "-", "_"
	countNumbers      int // 0-9
	countSpecialChars int // anything else
}

// Reset underlying path being consumed
func (t *tokenizer) Reset(path string) {
	t.i = 0
	t.j = 0
	t.path = path
}

// Next attempts to parse the next token, and returns true if a token was read
func (t *tokenizer) Next() bool {
	t.countNumbers = 0
	t.countAllowedChars = 0
	t.countSpecialChars = 0
	t.i = t.j + 1

	for t.j = t.i; t.j < len(t.path); t.j++ {
		c := t.path[t.j]

		if c == '/' {
			break
		} else if c >= '0' && c <= '9' {
			t.countNumbers++
		} else if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' || c == '_' {
			t.countAllowedChars++
		} else {
			t.countSpecialChars++
		}
	}

	return t.i < len(t.path)
}

// Value returns the current token along with its byte value
// Note that the byte value is only valid until the next call to `Reset()`
func (t *tokenizer) Value() (tokenType, string) {
	if t.i < 0 || t.j > len(t.path) || t.i >= t.j {
		return tokenUnknown, ""
	}

	return t.getType(), t.path[t.i:t.j]
}

func (t *tokenizer) getType() tokenType {
	// This matches segments like "v1"
	if t.countAllowedChars == 1 && t.countNumbers > 0 && t.path[t.i] == 'v' {
		return tokenAPIVersion
	}

	// A segment that contains one or more special characters or numbers is
	// considered a wildcard token
	if t.countSpecialChars > 0 || t.countNumbers > 0 {
		return tokenWildcard
	}

	// If the segment is comprised by only allowed chars, we classify it as a
	// string token which is preserved as it is by the quantizer
	if t.countAllowedChars > 0 && t.countSpecialChars == 0 && t.countNumbers == 0 {
		return tokenString
	}

	return tokenUnknown
}
