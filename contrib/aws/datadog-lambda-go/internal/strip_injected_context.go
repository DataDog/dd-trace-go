// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"bytes"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

const (
	stripInjectedContextEnvVar = "DD_TRACE_STRIP_INJECTED_CONTEXT"
)

var (
	// Datadog injects _datadog as a flat JSON object carrier in one of two shapes:
	//  1. Object detail:  "_datadog": {"x-datadog-trace-id":"...", ...}
	//  2. String detail:  \"_datadog\":{\"x-datadog-trace-id\":\"...\", ...}  (escaped in JSON string)
	// Carrier value is always a {...} object. No post-removal comma cleanup is performed
	// (avoids corrupting customer string values — see TestStripInjectedContext_regression).
	stripInjectedContextEnabledOnce sync.Once
	stripInjectedContextEnabledVal  bool
	datadogCarrierKeyBytes          = []byte(`"_datadog"`)
	datadogCarrierSubstrBytes       = []byte("_datadog")
	datadogCarrierEscapedKey        = []byte(`\"_datadog\"`)
)

type carrierKeyForm struct {
	key     []byte
	escaped bool // true for \"_datadog\" inside string-encoded detail
}

var datadogCarrierForms = []carrierKeyForm{
	{key: datadogCarrierKeyBytes, escaped: false},
	{key: datadogCarrierEscapedKey, escaped: true},
}

// StripInjectedContext removes injected _datadog propagation carriers from the
// Lambda payload before it reaches the user handler when
// DD_TRACE_STRIP_INJECTED_CONTEXT=true.
//
// Extension listeners must receive the raw payload first so APM trace extraction
// is unchanged. Returns the original message when the env var is unset/false,
// the payload contains no _datadog carrier, or stripping would produce invalid
// JSON (fail-open).
func StripInjectedContext(msg json.RawMessage) json.RawMessage {
	if !stripInjectedContextEnabled() {
		return msg
	}
	if len(msg) == 0 {
		return msg
	}
	if !bytes.Contains(msg, datadogCarrierSubstrBytes) {
		return msg
	}

	out, changed := stripInjectedContextBytes(msg)
	if !changed {
		return msg
	}
	if !json.Valid(out) {
		return msg // fail-open
	}
	return out
}

// stripInjectedContextEnabled reports whether stripping is enabled, caching the env lookup for the process lifetime.
func stripInjectedContextEnabled() bool {
	stripInjectedContextEnabledOnce.Do(func() {
		v, ok := env.Lookup(stripInjectedContextEnvVar)
		if !ok {
			return // unset -> opt-in off
		}
		enabled, err := strconv.ParseBool(v)
		stripInjectedContextEnabledVal = err == nil && enabled
	})
	return stripInjectedContextEnabledVal
}

// ResetStripInjectedContextCacheForTest clears the env cache so tests can change DD_TRACE_STRIP_INJECTED_CONTEXT.
func ResetStripInjectedContextCacheForTest() {
	stripInjectedContextEnabledOnce = sync.Once{}
	stripInjectedContextEnabledVal = false
}

// stripInjectedContextBytes removes every _datadog carrier from msg and reports whether the bytes changed.
func stripInjectedContextBytes(msg json.RawMessage) (json.RawMessage, bool) {
	out := msg
	changed := false

	for {
		start, end, ok := findCarrierRange(out)
		if !ok {
			break
		}
		out = append(append([]byte{}, out[:start]...), out[end:]...)
		changed = true
	}
	return out, changed
}

// findCarrierRange returns the byte span of the first removable _datadog key/value pair in b.
func findCarrierRange(b []byte) (start, end int, ok bool) {
	for _, form := range datadogCarrierForms {
		searchFrom := 0
		keyLen := len(form.key)
		for {
			keyIdx := bytes.Index(b[searchFrom:], form.key)
			if keyIdx < 0 {
				break
			}
			keyIdx += searchFrom

			if !isCarrierKeyAt(b, keyIdx, keyLen) {
				searchFrom = keyIdx + 1
				continue
			}

			valueEnd, ok := jsonCarrierValueEnd(b, keyIdx+keyLen, form.escaped)
			if !ok {
				searchFrom = keyIdx + 1
				continue
			}

			removeStart, removeEnd := expandRemovalRange(b, keyIdx, valueEnd)
			return removeStart, removeEnd, true
		}
	}
	return 0, 0, false
}

// isCarrierKeyAt reports whether keyIdx starts a JSON object key (preceded by '{' or ',', not a string value).
func isCarrierKeyAt(b []byte, keyIdx, keyLen int) bool {
	if keyIdx < 0 || keyIdx+keyLen > len(b) {
		return false
	}
	prev := keyIdx - 1
	for prev >= 0 && isJSONWhitespace(b[prev]) {
		prev--
	}
	return prev >= 0 && (b[prev] == '{' || b[prev] == ',')
}

// jsonCarrierValueEnd returns the byte offset after the {...} carrier value following a matched key.
func jsonCarrierValueEnd(b []byte, from int, escaped bool) (int, bool) {
	i := from
	for i < len(b) && isJSONWhitespace(b[i]) {
		i++
	}
	if i >= len(b) || b[i] != ':' {
		return 0, false
	}
	i++
	for i < len(b) && isJSONWhitespace(b[i]) {
		i++
	}
	if i >= len(b) || b[i] != '{' {
		return 0, false
	}
	if escaped {
		return scanDelimitedValueEndEscaped(b, i)
	}
	return scanDelimitedValueEnd(b, i, '{', '}')
}

// expandRemovalRange widens [keyStart:valueEnd) to include one adjacent structural comma when present.
func expandRemovalRange(b []byte, keyStart, valueEnd int) (removeStart, removeEnd int) {
	removeStart = keyStart
	removeEnd = valueEnd

	prev := keyStart - 1
	for prev >= 0 && isJSONWhitespace(b[prev]) {
		prev--
	}
	if prev >= 0 && b[prev] == ',' {
		return prev, removeEnd
	}

	next := removeEnd
	for next < len(b) && isJSONWhitespace(b[next]) {
		next++
	}
	if next < len(b) && b[next] == ',' {
		return removeStart, next + 1
	}
	return removeStart, removeEnd
}

// isJSONWhitespace checks if the given byte is a JSON whitespace character.
func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// scanDelimitedValueEnd scans for the end of a delimited value in the given byte slice.
func scanDelimitedValueEnd(b []byte, start int, open, close byte) (int, bool) {
	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(b); i++ {
		c := b[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// scanDelimitedValueEndEscaped returns the byte offset after a balanced {...} value with backslash-escaped bytes.
func scanDelimitedValueEndEscaped(b []byte, start int) (int, bool) {
	depth := 0
	for i := start; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			i++
			continue
		}
		switch b[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}
