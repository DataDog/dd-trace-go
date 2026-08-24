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
	if !json.Valid(msg) {
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

// Datadog injects _datadog as a flat JSON object carrier in one of two shapes:
//  1. Object detail:  "_datadog": {"x-datadog-trace-id":"...", ...}  — stripped via byte scan
//  2. String detail:  detail field contains "{\"...\",\"_datadog\":{...}}"  — stripped via json unmarshal/remarshal
const datadogCarrierKey = "_datadog"

var (
	datadogKeyColon           = []byte(`"` + datadogCarrierKey + `":`)
	datadogCarrierSubstrBytes = []byte(datadogCarrierKey)
	datadogCarrierEscapedKey  = []byte(`\"` + datadogCarrierKey + `\"`)
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
// Writes into a freshly allocated buffer; msg is never mutated, so callers keep a safe,
// untouched copy even when the result is later discarded (e.g. by the fail-open path).
func stripObjectCarriers(msg json.RawMessage) (json.RawMessage, bool) {
	type span struct{ lo, hi int }
	var spans []span
	searchFrom := 0

	for {
		rel := bytes.Index(msg[searchFrom:], datadogKeyColon)
		if rel < 0 {
			break
		}
		keyAt := searchFrom + rel

		// Must be preceded (ignoring whitespace) by '{' or ','
		p := keyAt - 1
		for p >= 0 && isJSONWhitespace(msg[p]) {
			p--
		}
		if p < 0 || (msg[p] != '{' && msg[p] != ',') {
			searchFrom = keyAt + 1
			continue
		}

		// Value must open with '{'
		i := keyAt + len(datadogKeyColon)
		for i < len(msg) && isJSONWhitespace(msg[i]) {
			i++
		}
		if i >= len(msg) || msg[i] != '{' {
			searchFrom = keyAt + 1
			continue
		}

		valHi, ok := scanObjectEnd(msg, i)
		if !ok {
			searchFrom = keyAt + 1
			continue
		}

		// Absorb one adjacent comma. Backward search must not cross searchFrom
		// so two consecutive carriers cannot both claim the same separating comma.
		lo, hi := keyAt, valHi
		bk := keyAt - 1
		for bk >= searchFrom && isJSONWhitespace(msg[bk]) {
			bk--
		}
		if bk >= searchFrom && msg[bk] == ',' {
			lo = bk
		} else {
			fwd := valHi
			for fwd < len(msg) && isJSONWhitespace(msg[fwd]) {
				fwd++
			}
			if fwd < len(msg) && msg[fwd] == ',' {
				hi = fwd + 1
			}
		}

		spans = append(spans, span{lo, hi})
		searchFrom = hi
	}

	if len(spans) == 0 {
		return msg, false
	}

	out := make([]byte, 0, len(msg))
	prev := 0
	for _, s := range spans {
		out = append(out, msg[prev:s.lo]...)
		prev = s.hi
	}
	return append(out, msg[prev:]...), true
}

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
		newVal, ok := stripDatadogFromStringField(rawVal)
		if !ok {
			continue
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

// stripDatadogFromStringField removes the _datadog key from the JSON object
// encoded within a JSON string field. Returns the new field value and true
// if the field was modified, or nil and false otherwise.
func stripDatadogFromStringField(rawVal json.RawMessage) (json.RawMessage, bool) {
	var s string
	if err := json.Unmarshal(rawVal, &s); err != nil {
		return nil, false // not a string field
	}
	if !bytes.Contains([]byte(s), datadogCarrierSubstrBytes) {
		return nil, false
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &inner); err != nil {
		return nil, false // string is not a JSON object
	}
	if _, ok := inner[datadogCarrierKey]; !ok {
		return nil, false
	}
	delete(inner, datadogCarrierKey)
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		return nil, false
	}
	newVal, err := json.Marshal(string(innerBytes))
	if err != nil {
		return nil, false
	}
	return newVal, true
}

// isJSONWhitespace checks if the given byte is a JSON whitespace character.
func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// scanObjectEnd returns the byte offset after the closing '}' of a JSON object
// starting at start in b, correctly tracking nested objects and strings.
func scanObjectEnd(b []byte, start int) (int, bool) {
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