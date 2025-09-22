// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package streamprocessingoffload

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/negasus/haproxy-spoe-go/varint"
	"github.com/stretchr/testify/require"
)

func TestParseHAProxyReqHdrsBin_Table(t *testing.T) {
	longName := string(bytes.Repeat([]byte("N"), 300))
	longValue := string(bytes.Repeat([]byte("V"), 500))

	tests := []struct {
		name    string
		build   func() []byte
		want    http.Header
		wantErr bool
		check   func(t *testing.T, got http.Header)
	}{
		{
			name:    "empty-buffer-error",
			build:   func() []byte { return nil },
			wantErr: true,
		},
		{
			name:  "termination-only-empty-headers",
			build: func() []byte { return encodeTerminator(nil) },
			want:  http.Header{},
		},
		{
			name: "single-header",
			build: func() []byte {
				var b []byte
				b = encodeHeader(b, "Host", "example.com")
				return encodeTerminator(b)
			},
			want: http.Header{"Host": {"example.com"}},
		},
		{
			name: "multiple-headers-and-duplicates",
			build: func() []byte {
				var b []byte
				b = encodeHeader(b, "X-A", "1")
				b = encodeHeader(b, "X-A", "2")
				b = encodeHeader(b, "Y", "z")
				return encodeTerminator(b)
			},
			want: http.Header{"X-A": {"1", "2"}, "Y": {"z"}},
		},
		{
			name: "empty-value-allowed",
			build: func() []byte {
				var b []byte
				b = encodeHeader(b, "X-Empty", "")
				return encodeTerminator(b)
			},
			want: http.Header{"X-Empty": {""}},
		},
		{
			name: "multi-byte-varint-lengths",
			build: func() []byte {
				var b []byte
				b = encodeHeader(b, longName, longValue)
				return encodeTerminator(b)
			},
			check: func(t *testing.T, got http.Header) {
				if len(got.Get(longName)) != len(longValue) {
					t.Fatalf("expected value length %d, got %d", len(longValue), len(got.Get(longName)))
				}
			},
		},
		{
			name:    "malformed-truncated-varint-for-name",
			build:   func() []byte { return []byte{0xF0} }, // >=240 => truncated
			wantErr: true,
		},
		{
			name: "malformed-empty-name-non-empty-value",
			build: func() []byte {
				var tmp [10]byte
				var b []byte
				// name len = 0
				n := varint.PutUvarint(tmp[:], 0)
				b = append(b, tmp[:n]...)
				// value len = 1 + value byte
				n = varint.PutUvarint(tmp[:], 1)
				b = append(b, tmp[:n]...)
				b = append(b, 'x')
				return b
			},
			wantErr: true,
		},
		{
			name: "name-exceeds-remaining-error",
			build: func() []byte {
				var tmp [10]byte
				var b []byte
				n := varint.PutUvarint(tmp[:], 10)
				b = append(b, tmp[:n]...)
				b = append(b, []byte("short")...) // only 5 bytes
				return b
			},
			wantErr: true,
		},
		{
			name: "value-exceeds-remaining-error",
			build: func() []byte {
				var b []byte
				b = encodeHeader(b, "K", "V")
				// corrupt last byte
				return b[:len(b)-1]
			},
			wantErr: true,
		},
		{
			name: "ignores-trailing-bytes-after-terminator",
			build: func() []byte {
				var b []byte
				b = encodeHeader(b, "A", "B")
				b = encodeTerminator(b)
				b = append(b, 0xFF, 0xEE, 0xDD) // trailing garbage
				return b
			},
			want: http.Header{"A": {"B"}},
		},
		{
			name:    "malformed-empty",
			build:   func() []byte { return nil },
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHAProxyReqHdrsBin(tc.build())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, got)
				return
			}

			require.True(t, reflect.DeepEqual(got, tc.want), fmt.Sprintf("expected headers %v, got %v", tc.want, got))
		})
	}
}

// FuzzParseHAProxyReqHdrsBin ensures the decoder never panics/hangs on arbitrary inputs.
func FuzzParseHAProxyReqHdrsBin(f *testing.F) {
	f.Add(encodeTerminator(nil))

	// Single header
	var b1 []byte
	b1 = encodeHeader(b1, "Host", "example.com")
	b1 = encodeTerminator(b1)
	f.Add(b1)

	// Duplicates
	var b2 []byte
	b2 = encodeHeader(b2, "X-A", "1")
	b2 = encodeHeader(b2, "X-A", "2")
	b2 = encodeTerminator(b2)
	f.Add(b2)

	// Long multi-byte varints
	longName := string(bytes.Repeat([]byte("N"), 300))
	longValue := string(bytes.Repeat([]byte("V"), 500))
	var b3 []byte
	b3 = encodeHeader(b3, longName, longValue)
	b3 = encodeTerminator(b3)
	f.Add(b3)

	// Malformed: truncated varint
	f.Add([]byte{0xF0})

	// Malformed: empty name, non-empty value
	var tmp [10]byte
	var b4 []byte
	n := varint.PutUvarint(tmp[:], 0)
	b4 = append(b4, tmp[:n]...)
	n = varint.PutUvarint(tmp[:], 1)
	b4 = append(b4, tmp[:n]...)
	b4 = append(b4, 'x')
	f.Add(b4)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Bound the size to avoid excessive memory/time usage
		if len(data) > 1<<16 { // 64KiB cap keeps runs fast
			t.Skip()
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()

		_, _ = parseHAProxyReqHdrsBin(data)
	})
}

// encodeHeader appends a single <name,value> pair using SPOE varint length-prefixed strings.
func encodeHeader(dst []byte, name, value string) []byte {
	var buf [10]byte
	n := varint.PutUvarint(buf[:], uint64(len(name)))
	dst = append(dst, buf[:n]...)
	dst = append(dst, name...)

	n = varint.PutUvarint(buf[:], uint64(len(value)))
	dst = append(dst, buf[:n]...)
	dst = append(dst, value...)
	return dst
}

// encodeTerminator appends the terminating empty name/value pair.
func encodeTerminator(dst []byte) []byte {
	var buf [10]byte
	n := varint.PutUvarint(buf[:], 0)
	dst = append(dst, buf[:n]...)
	n = varint.PutUvarint(buf[:], 0)
	dst = append(dst, buf[:n]...)
	return dst
}
