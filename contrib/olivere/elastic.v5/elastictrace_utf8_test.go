// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// multiByteRunes holds one rune of each multi-byte UTF-8 encoding width.
var multiByteRunes = []string{
	"é", // U+00E9, 2 bytes
	"€", // U+20AC, 3 bytes
	"😀", // U+1F600, 4 bytes
}

// TestPeekUTF8 is a regression test: cutting a valid UTF-8 body at a fixed byte limit must
// never leave a partial rune at the end of the snippet.
func TestPeekUTF8(t *testing.T) {
	const prefix = "ab"
	for _, r := range multiByteRunes {
		require.Equal(t, len(r), utf8.RuneLen([]rune(r)[0]))
		// k is the number of bytes of r that fit under the limit.
		for k := 0; k <= len(r); k++ {
			want := prefix
			if k == len(r) {
				want += r
			}
			limit := len(prefix) + k
			body := prefix + r + "cd"

			t.Run("raw/"+strconv.Itoa(len(r))+"-byte/"+strconv.Itoa(k), func(t *testing.T) {
				snip, rc, err := peek(io.NopCloser(strings.NewReader(body)), "", -1, limit)
				require.NoError(t, err)
				assert.Equal(t, want, snip)
				assert.True(t, utf8.ValidString(snip))
				assert.LessOrEqual(t, len(snip), limit)
				all, err := io.ReadAll(rc)
				require.NoError(t, err)
				assert.Equal(t, body, string(all), "the forwarded body must be unchanged")
			})

			t.Run("content-length/"+strconv.Itoa(len(r))+"-byte/"+strconv.Itoa(k), func(t *testing.T) {
				// A Content-Length shorter than the cutoff lowers the limit instead.
				snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "", limit, limit+100)
				require.NoError(t, err)
				assert.Equal(t, want, snip)
			})

			t.Run("gzip/"+strconv.Itoa(len(r))+"-byte/"+strconv.Itoa(k), func(t *testing.T) {
				// The decompressed snippet is cut at the cutoff, not at the compressed length.
				const cutoff = 1024
				pad := strings.Repeat("a", cutoff-k)
				gz := gzipped(t, []byte(pad+r+strings.Repeat("b", cutoff)))
				require.Less(t, len(gz), cutoff)

				snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), cutoff)
				require.NoError(t, err)
				gzWant := pad
				if k == len(r) {
					gzWant += r
				}
				assert.Equal(t, gzWant, snip)
				assert.True(t, utf8.ValidString(snip))
			})
		}

		t.Run("not-gzip/"+strconv.Itoa(len(r))+"-byte", func(t *testing.T) {
			// A body labeled gzip that isn't is returned raw, and is cut the same way.
			body := prefix + r + "cd"
			snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "gzip", -1, len(prefix)+1)
			require.NoError(t, err)
			assert.Equal(t, prefix, snip)
		})
	}

	t.Run("ascii", func(t *testing.T) {
		snip, _, err := peek(io.NopCloser(strings.NewReader("ABCDEFG")), "", -1, 4)
		require.NoError(t, err)
		assert.Equal(t, "ABCD", snip)
	})

	t.Run("exact boundary", func(t *testing.T) {
		for _, r := range multiByteRunes {
			body := "ab" + r
			snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "", -1, len(body))
			require.NoError(t, err)
			assert.Equal(t, body, snip)

			snip, _, err = peek(io.NopCloser(strings.NewReader(body)), "", len(body), len(body))
			require.NoError(t, err)
			assert.Equal(t, body, snip)
		}
	})

	t.Run("shorter than limit", func(t *testing.T) {
		body := "ab😀"
		snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "", -1, 100)
		require.NoError(t, err)
		assert.Equal(t, body, snip)
	})

	t.Run("invalid input is not rewritten", func(t *testing.T) {
		// Only a rune cut by the limit is dropped; malformed bytes are left as they were.
		body := "ab\xff\xfe\xfd\xfccd"
		snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "", -1, 6)
		require.NoError(t, err)
		assert.Equal(t, body[:6], snip)
	})
}

// TestHTTPClientUTF8BodyCutoff checks that the elasticsearch.body and error.message tags stay
// valid UTF-8 when a multi-byte rune straddles bodyCutoff.
func TestHTTPClientUTF8BodyCutoff(t *testing.T) {
	for _, r := range multiByteRunes {
		for k := 0; k <= len(r); k++ {
			t.Run(strconv.Itoa(len(r))+"-byte/"+strconv.Itoa(k), func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				body := strings.Repeat("a", bodyCutoff-k) + r + strings.Repeat("b", 64)
				want := bodyCutoff - k
				if k == len(r) {
					want = bodyCutoff
				}

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					got, _ := io.ReadAll(req.Body)
					assert.Equal(t, body, string(got), "the forwarded request body must be unchanged")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, body)
				}))
				defer srv.Close()

				req, err := http.NewRequest(http.MethodPost, srv.URL+"/twitter/_search", strings.NewReader(body))
				require.NoError(t, err)
				res, err := NewHTTPClient().Do(req)
				require.NoError(t, err)
				got, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				res.Body.Close()
				assert.Equal(t, body, string(got), "the response body must be unchanged")

				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				for _, key := range []string{"elasticsearch.body", ext.ErrorMsg} {
					tag := spans[0].Tag(key).(string)
					assert.True(t, utf8.ValidString(tag), "%s ends in %q", key, tag[max(0, len(tag)-4):])
					assert.Len(t, tag, want, key)
					assert.True(t, strings.HasPrefix(body, tag), key)
				}
			})
		}
	}
}

func BenchmarkPeek(b *testing.B) {
	for _, bc := range []struct {
		name string
		body string
	}{
		{"ascii", strings.Repeat("a", 2*bodyCutoff)},
		{"multibyte", strings.Repeat("€", bodyCutoff)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = peek(io.NopCloser(strings.NewReader(bc.body)), "", -1, bodyCutoff)
			}
		})
	}
}
