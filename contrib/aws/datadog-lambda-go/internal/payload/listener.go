// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package payload

import (
	"bytes"
	"context"
	"encoding/json"
)

const (
	StripInjectedContextEnvVar = "DD_LAMBDA_STRIP_INJECTED_CONTEXT"
)

type (
	// Listener implements wrapper.HandlerListener, stripping injected _datadog
	// propagation carriers from the Lambda payload before it reaches the user handler, when enabled.
	Listener struct {
		enabled bool
	}
	// Config gives options for how the listener should work
	Config struct {
		StripInjectedContext bool
	}
)

// MakeListener creates a new Listener with the given Config
func MakeListener(config Config) Listener {
	return Listener{enabled: config.StripInjectedContext}
}

// HandlerStarted strips injected _datadog carriers from msg for the handler when enabled.
//
// Extension listeners must run before this one so APM trace extraction sees
// the raw payload; stripping only affects what reaches the user handler.
// Returns msg unchanged if disabled, no carrier is present, or stripping
// would produce invalid JSON (fail-open).
func (l *Listener) HandlerStarted(ctx context.Context, msg json.RawMessage) (context.Context, json.RawMessage) {
	if !l.enabled || len(msg) == 0 {
		return ctx, msg
	}
	if !bytes.Contains(msg, datadogCarrierSubstrBytes) {
		return ctx, msg
	}

	out, changed := stripInjectedContextBytes(msg)
	if !changed {
		return ctx, msg
	}
	if !json.Valid(out) {
		return ctx, msg // fail-open
	}
	return ctx, out
}

// HandlerFinished implemented as part of the wrapper.HandlerListener interface
func (l *Listener) HandlerFinished(ctx context.Context, err error) {}

var (
	// Datadog injects _datadog as a flat JSON object carrier in one of two shapes:
	//  1. Object detail:  "_datadog": {"x-datadog-trace-id":"...", ...}  — stripped via byte scan
	//  2. String detail:  detail field contains "{\"...\",\"_datadog\":{...}}"  — stripped via json unmarshal/remarshal
	datadogCarrierKeyBytes    = []byte(`"_datadog"`)
	datadogCarrierSubstrBytes = []byte("_datadog")
	datadogCarrierEscapedKey  = []byte(`\"_datadog\"`)
)

// stripInjectedContextBytes removes every _datadog carrier from msg and reports whether the bytes changed.
func stripInjectedContextBytes(msg json.RawMessage) (json.RawMessage, bool) {
	out, objectChanged := stripObjectCarriers(msg)
	out2, stringChanged := stripStringEncodedCarriers(out)
	if stringChanged {
		return out2, true
	}
	return out, objectChanged
}

// stripObjectCarriers strips object-form "_datadog" carriers via byte scanning, supporting multiple carriers in O(n).
func stripObjectCarriers(msg json.RawMessage) (json.RawMessage, bool) {
	type span struct{ start, end int }
	var ranges []span

	searchFrom := 0
	for {
		start, end, ok := findCarrierRange(msg, searchFrom)
		if !ok {
			break
		}
		ranges = append(ranges, span{start, end})
		searchFrom = end
	}
	if len(ranges) == 0 {
		return msg, false
	}
	if len(ranges) == 1 {
		r := ranges[0]
		return append(msg[:r.start], msg[r.end:]...), true
	}
	out := msg
	for i := len(ranges) - 1; i >= 0; i-- {
		r := ranges[i]
		out = append(out[:r.start], out[r.end:]...)
	}
	return out, true
}

const datadogCarrierKey = "_datadog"

// stripStringEncodedCarriers removes _datadog from JSON objects embedded in top-level string fields.
func stripStringEncodedCarriers(msg json.RawMessage) (json.RawMessage, bool) {
	if !bytes.Contains(msg, datadogCarrierEscapedKey) {
		return msg, false
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(msg, &envelope); err != nil {
		return msg, false
	}

	changed := false
	for key, rawVal := range envelope {
		var s string
		if err := json.Unmarshal(rawVal, &s); err != nil {
			continue // not a string field
		}
		if !bytes.Contains([]byte(s), datadogCarrierSubstrBytes) {
			continue
		}

		var inner map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &inner); err != nil {
			continue // string is not a JSON object
		}
		if _, ok := inner[datadogCarrierKey]; !ok {
			continue
		}

		delete(inner, datadogCarrierKey)
		innerBytes, err := json.Marshal(inner)
		if err != nil {
			return msg, false
		}
		newVal, err := json.Marshal(string(innerBytes))
		if err != nil {
			return msg, false
		}
		envelope[key] = newVal
		changed = true
	}

	if !changed {
		return msg, false
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return msg, false
	}
	return out, true
}

// findCarrierRange returns the byte span of the first removable _datadog key/value pair in b at or after searchFrom.
func findCarrierRange(b []byte, searchFrom int) (start, end int, ok bool) {
	keyLen := len(datadogCarrierKeyBytes)
	for {
		keyIdx := bytes.Index(b[searchFrom:], datadogCarrierKeyBytes)
		if keyIdx < 0 {
			return 0, 0, false
		}
		keyIdx += searchFrom

		if !isCarrierKeyAt(b, keyIdx, keyLen) {
			searchFrom = keyIdx + 1
			continue
		}

		valueEnd, ok := jsonCarrierValueEnd(b, keyIdx+keyLen)
		if !ok {
			searchFrom = keyIdx + 1
			continue
		}

		removeStart, removeEnd := expandRemovalRange(b, keyIdx, valueEnd)
		return removeStart, removeEnd, true
	}
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
func jsonCarrierValueEnd(b []byte, from int) (int, bool) {
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
