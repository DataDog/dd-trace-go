// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package payload

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

type (
	// Listener implements wrapper.HandlerListener, stripping injected _datadog
	// propagation carriers from the Lambda payload before it reaches the user handler, when enabled.
	Listener struct {
		enabled bool
	}

	// Config gives options for how the listener should work.
	Config struct {
		StripInjectedContext bool
	}
)

// byteRange represents a half-open byte range [start, end) to remove.
type byteRange struct {
	start int
	end   int
}

// datadogCarrierKey is the JSON property name for the Datadog propagation carrier.
const datadogCarrierKey = "_datadog"

var (
	datadogKeyColon           = []byte(`"` + datadogCarrierKey + `":`)
	datadogCarrierSubstrBytes = []byte(datadogCarrierKey)
	datadogCarrierEscapedKey  = []byte(`\"` + datadogCarrierKey + `\"`)
)

// MakeListener creates a new Listener with the given Config.
func MakeListener(config Config) Listener {
	return Listener{enabled: config.StripInjectedContext}
}

// HandlerStarted strips injected _datadog carriers from msg for the handler when enabled.
//
// Extension listeners must run before this one so APM trace extraction sees
// the raw payload; stripping only affects what reaches the user handler.
// Returns msg unchanged if stripping fails or an unexpected panic occurs.
func (l *Listener) HandlerStarted(ctx context.Context, msg json.RawMessage) (retCtx context.Context, retMsg json.RawMessage) {
	retCtx, retMsg = ctx, msg

	// Fail open so payload stripping never prevents the user handler from running.
	defer func() {
		if recover() != nil {
			retCtx = ctx
			retMsg = msg
		}
	}()

	if !l.enabled || len(msg) == 0 {
		return
	}
	if !bytes.Contains(msg, datadogCarrierSubstrBytes) {
		return
	}
	if !json.Valid(msg) {
		return
	}

	stripped, changed := stripInjectedContextBytes(msg)
	if !changed || !json.Valid(stripped) {
		return
	}

	retMsg = stripped
	return
}

// HandlerFinished implements the wrapper.HandlerListener interface.
func (l *Listener) HandlerFinished(ctx context.Context, err error) {}

// stripInjectedContextBytes removes supported _datadog carriers from msg.
// Datadog injects _datadog in one of two shapes:
//  1. Object detail: "_datadog": {...}
//  2. String detail: "detail": "{\"_datadog\":{...}}"
func stripInjectedContextBytes(msg json.RawMessage) (json.RawMessage, bool) {
	out, objectChanged := stripObjectCarriers(msg)
	out, stringChanged := stripStringEncodedCarriers(out)

	return out, objectChanged || stringChanged
}

// stripObjectCarriers removes object-form "_datadog" carriers via byte scanning.
// The result is built incrementally to avoid allocating a separate range slice.
func stripObjectCarriers(msg json.RawMessage) (json.RawMessage, bool) {
	var out []byte
	searchFrom := 0
	copyFrom := 0

	for searchFrom < len(msg) {
		keyStart, valueEnd, found := findNextObjectCarrier(msg, searchFrom)
		if !found {
			break
		}

		removal := carrierRemovalRange(msg, keyStart, valueEnd, searchFrom)
		if out == nil {
			// Allocate only after finding the first removable carrier. Capacity is
			// based on the input size so multiple carriers do not grow the buffer.
			out = make([]byte, 0, len(msg))
		}

		out = append(out, msg[copyFrom:removal.start]...)
		copyFrom = removal.end
		searchFrom = removal.end
	}

	if out == nil {
		return msg, false
	}

	out = append(out, msg[copyFrom:]...)
	return out, true
}

// findNextObjectCarrier returns the key start and object-value end for the
// next removable carrier at or after searchFrom.
func findNextObjectCarrier(msg []byte, searchFrom int) (keyStart, valueEnd int, found bool) {
	for searchFrom < len(msg) {
		relative := bytes.Index(msg[searchFrom:], datadogKeyColon)
		if relative < 0 {
			return 0, 0, false
		}

		keyStart = searchFrom + relative
		if !isObjectProperty(msg, keyStart) {
			searchFrom = keyStart + 1
			continue
		}

		valueStart, ok := objectValueStart(msg, keyStart+len(datadogKeyColon))
		if !ok {
			searchFrom = keyStart + 1
			continue
		}

		valueEnd, ok = scanObjectEnd(msg, valueStart)
		if !ok {
			searchFrom = keyStart + 1
			continue
		}

		return keyStart, valueEnd, true
	}

	return 0, 0, false
}

// isObjectProperty reports whether keyStart is positioned after an object
// opening brace or an object-property separator.
func isObjectProperty(msg []byte, keyStart int) bool {
	previous := skipJSONWhitespaceBackward(msg, keyStart-1)
	return previous >= 0 && (msg[previous] == '{' || msg[previous] == ',')
}

// objectValueStart returns the opening brace for an object carrier value.
func objectValueStart(msg []byte, start int) (int, bool) {
	start = skipJSONWhitespaceForward(msg, start)
	return start, start < len(msg) && msg[start] == '{'
}

// carrierRemovalRange expands a carrier range to consume one adjacent comma.
// It prefers the preceding comma unless that comma belongs to an earlier range.
func carrierRemovalRange(msg []byte, keyStart, valueEnd, searchFrom int) byteRange {
	previous := skipJSONWhitespaceBackward(msg, keyStart-1)
	if previous >= searchFrom && msg[previous] == ',' {
		return byteRange{start: previous, end: valueEnd}
	}

	next := skipJSONWhitespaceForward(msg, valueEnd)
	if next < len(msg) && msg[next] == ',' {
		return byteRange{start: keyStart, end: next + 1}
	}

	return byteRange{start: keyStart, end: valueEnd}
}

// stripStringEncodedCarriers removes _datadog from JSON objects encoded
// inside top-level string fields.
func stripStringEncodedCarriers(msg json.RawMessage) (json.RawMessage, bool) {
	if !bytes.Contains(msg, datadogCarrierEscapedKey) {
		return msg, false
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(msg, &envelope); err != nil {
		return msg, false
	}

	changed := false
	for key, rawValue := range envelope {
		newValue, ok := stripDatadogFromStringField(rawValue)
		if !ok {
			continue
		}

		envelope[key] = newValue
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

// stripDatadogFromStringField removes _datadog from a JSON object encoded
// inside a JSON string.
func stripDatadogFromStringField(rawValue json.RawMessage) (json.RawMessage, bool) {
	var value string
	if err := json.Unmarshal(rawValue, &value); err != nil {
		return nil, false
	}
	if !strings.Contains(value, datadogCarrierKey) {
		return nil, false
	}

	var inner map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &inner); err != nil {
		return nil, false
	}
	if _, exists := inner[datadogCarrierKey]; !exists {
		return nil, false
	}

	delete(inner, datadogCarrierKey)

	innerBytes, err := json.Marshal(inner)
	if err != nil {
		return nil, false
	}

	newValue, err := json.Marshal(string(innerBytes))
	if err != nil {
		return nil, false
	}

	return newValue, true
}

// skipJSONWhitespaceForward returns the first index at or after start that is
// not JSON whitespace. Returns len(msg) when only whitespace remains.
func skipJSONWhitespaceForward(msg []byte, start int) int {
	for start < len(msg) && isJSONWhitespace(msg[start]) {
		start++
	}
	return start
}

// skipJSONWhitespaceBackward returns the first index at or before start that is
// not JSON whitespace. Returns a value less than zero when only whitespace remains.
func skipJSONWhitespaceBackward(msg []byte, start int) int {
	for start >= 0 && isJSONWhitespace(msg[start]) {
		start--
	}
	return start
}

// isJSONWhitespace reports whether c is JSON whitespace.
func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// scanObjectEnd returns the offset after the closing '}' of the object at start,
// accounting for nested objects and quoted strings.
func scanObjectEnd(msg []byte, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(msg); i++ {
		current := msg[i]

		if inString { // Order is significant: escaped must be checked before '\\' and '"'
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == '"' {
				inString = false
			}
			continue
		}

		switch current { // Outside a string, '{' and '}' drive the depth counter
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
