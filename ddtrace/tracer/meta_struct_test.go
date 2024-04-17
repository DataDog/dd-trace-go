// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tinylib/msgp/msgp"
)

type badWriter struct{}

func (bw *badWriter) Write(_ []byte) (n int, err error) {
	return 0, errors.New("bad writer error")
}

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
			encodingError: errors.New("msgp: type \"chan struct {}\" not supported at MetaStruct/key"),
		},
		{
			name: "encoding-error/channel",
			input: metaStructMap{
				"key": make(chan struct{}),
			},
			encodingError: errors.New("msgp: type \"chan struct {}\" not supported at MetaStruct/key"),
		},
		{
			name: "encoding-error/func",
			input: metaStructMap{
				"key": func() {},
			},
			encodingError: errors.New("msgp: type \"func()\" not supported at MetaStruct/key"),
		},
	} {
		t.Run(tc.name+"/serdes", func(t *testing.T) {
			// Encode the metaStructMap
			var buf bytes.Buffer
			enc := msgp.NewWriter(&buf)
			err := tc.input.EncodeMsg(enc)
			require.NoError(t, enc.Flush())
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

		t.Run(tc.name+"/bad-writer", func(t *testing.T) {
			// Encode the metaStructMap
			enc := msgp.NewWriter(&badWriter{})
			err := tc.input.EncodeMsg(enc)

			if tc.encodingError != nil {
				require.EqualError(t, err, tc.encodingError.Error())
				return
			}

			require.EqualError(t, enc.Flush(), "bad writer error")
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
