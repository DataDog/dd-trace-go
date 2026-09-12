// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var errStrictJSONNotObject = errors.New("strict_json_not_object")

func decodeStrictObject(raw []byte, maxBytes int, invalidErr, trailingErr, duplicateErr error) (map[string]json.RawMessage, error) {
	if len(raw) > maxBytes || !utf8.Valid(raw) {
		return nil, invalidErr
	}
	dec := json.NewDecoder(bytesReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, invalidErr
	}
	if delimiter, ok := tok.(json.Delim); !ok || delimiter != '{' {
		return nil, errStrictJSONNotObject
	}
	seen := map[string]json.RawMessage{}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, invalidErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, invalidErr
		}
		if _, exists := seen[key]; exists {
			return nil, duplicateErr
		}
		var rawValue json.RawMessage
		if err := dec.Decode(&rawValue); err != nil {
			return nil, invalidErr
		}
		seen[key] = rawValue
	}
	if tok, err = dec.Token(); err != nil {
		return nil, invalidErr
	}
	if delimiter, ok := tok.(json.Delim); !ok || delimiter != '}' {
		return nil, invalidErr
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, trailingErr
	}
	return seen, nil
}

func stringField(fields map[string]json.RawMessage, key string, missingErr, wrongTypeErr error) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", missingErr
	}
	if len(raw) == 0 || raw[0] != '"' {
		return "", wrongTypeErr
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", wrongTypeErr
	}
	return value, nil
}

type byteSliceReader struct {
	data []byte
	off  int64
}

func bytesReader(data []byte) *byteSliceReader {
	return &byteSliceReader{data: data}
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += int64(n)
	return n, nil
}
