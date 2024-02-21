// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"errors"
	"github.com/stretchr/testify/require"
	"reflect"
	"testing"

	"github.com/tinylib/msgp/msgp"
)

func TestMetaStructMap_EncodeDecode(t *testing.T) {
	// Create a sample metaStructMap
	meta := map[string]any{
		"key1": "value1",
		"key2": "value2",
	}

	for _, tc := range []struct {
		name          string
		input         metaStructMap
		encodingError error
		output        map[string]any
		decodingError error
	}{
		{
			name:   "empty",
			input:  metaStructMap{},
			output: map[string]any{},
		},
		{
			name:   "non-empty",
			input:  meta,
			output: meta,
		},
		{
			name:   "nil",
			input:  nil,
			output: nil,
		},
		{
			name: "nested-map",
			input: metaStructMap{
				"key": map[string]any{
					"nested-key": "nested-value",
				},
			},
			output: map[string]any{
				"key": map[string]any{
					"nested-key": "nested-value",
				},
			},
		},
		{
			name: "nested-slice",
			input: metaStructMap{
				"key": []any{
					"nested-value",
				},
			},
			output: map[string]any{
				"key": []any{
					"nested-value",
				},
			},
		},
		{
			name: "encoding-error/nested-chan",
			input: metaStructMap{
				"key": map[string]any{
					"nested-key": make(chan struct{}),
				},
			},
			encodingError: errors.New("msgp: type \"chan struct {}\" not supported"),
		},
		{
			name: "encoding-error/channel",
			input: metaStructMap{
				"key": make(chan struct{}),
			},
			encodingError: errors.New("msgp: type \"chan struct {}\" not supported"),
		},
		{
			name: "encoding-error/func",
			input: metaStructMap{
				"key": func() {},
			},
			encodingError: errors.New("msgp: type \"func()\" not supported"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Encode the metaStructMap
			var buf bytes.Buffer
			enc := msgp.NewWriter(&buf)
			err := tc.input.EncodeMsg(enc)
			if tc.encodingError != nil {
				require.EqualError(t, err, tc.encodingError.Error())
				return
			}

			require.NoError(t, err)
			require.NoError(t, enc.Flush())

			// Decode the encoded metaStructMap
			dec := msgp.NewReader(bytes.NewReader(buf.Bytes()))
			var decodedMeta metaStructMap
			err = decodedMeta.DecodeMsg(dec)
			if tc.decodingError != nil {
				require.EqualError(t, err, tc.decodingError.Error())
				return
			}

			require.NoError(t, err)

			// Compare the original and decoded metaStructMap
			compareMetaStructMaps(t, tc.output, decodedMeta)
		})
	}
}

func compareMetaStructMaps(t *testing.T, m1, m2 metaStructMap) {
	require.Equal(t, len(m1), len(m2), "mismatched map lengths: %d != %d", len(m1), len(m2))

	for k, v := range m1 {
		m2v, ok := m2[k]
		require.Truef(t, ok, "missing key %s", k)

		if !reflect.DeepEqual(v, m2v) {
			require.Fail(t, "compareMetaStructMaps", "mismatched key %s: expected '%v' but got '%v'", k, v, m2v)
		}
	}
}
