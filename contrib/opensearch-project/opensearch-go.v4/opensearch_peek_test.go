// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package opensearch

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// multiByteRunes holds one rune of each multi-byte UTF-8 encoding width.
var multiByteRunes = []string{
	"é", // U+00E9, 2 bytes
	"€", // U+20AC, 3 bytes
	"😀", // U+1F600, 4 bytes
}

// gzipBytes returns b compressed as a single gzip member.
func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	require.NoError(t, err)
	_, err = zw.Write(b)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestPeekUTF8 is a regression test: cutting a valid UTF-8 body at a fixed byte limit must
// never leave a partial rune at the end of the snippet.
func TestPeekUTF8(t *testing.T) {
	const prefix = "ab"
	for _, r := range multiByteRunes {
		// k is the number of bytes of r that fit under the limit.
		for k := 0; k <= len(r); k++ {
			want := prefix
			if k == len(r) {
				want += r
			}
			limit := len(prefix) + k
			body := prefix + r + "cd"
			name := strconv.Itoa(len(r)) + "-byte/" + strconv.Itoa(k)

			t.Run("raw/"+name, func(t *testing.T) {
				snip, rc, err := peek(io.NopCloser(strings.NewReader(body)), "", -1, limit)
				require.NoError(t, err)
				assert.Equal(t, want, snip)
				all, err := io.ReadAll(rc)
				require.NoError(t, err)
				assert.Equal(t, body, string(all), "the forwarded body must be unchanged")
			})

			t.Run("content-length/"+name, func(t *testing.T) {
				// A Content-Length shorter than the cutoff lowers the limit instead.
				snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "", limit, limit+100)
				require.NoError(t, err)
				assert.Equal(t, want, snip)
			})

			t.Run("gzip/"+name, func(t *testing.T) {
				// The decompressed snippet is cut at the cutoff, not at the compressed length.
				const cutoff = 1024
				pad := strings.Repeat("a", cutoff-k)
				gz := gzipBytes(t, []byte(pad+r+strings.Repeat("b", cutoff)))
				require.Less(t, len(gz), cutoff)
				snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), cutoff)
				require.NoError(t, err)
				gzWant := pad
				if k == len(r) {
					gzWant += r
				}
				assert.Equal(t, gzWant, snip)
			})
		}

		t.Run("not-gzip/"+strconv.Itoa(len(r))+"-byte", func(t *testing.T) {
			// A body labeled gzip that isn't is returned raw, and is cut the same way.
			snip, _, err := peek(io.NopCloser(strings.NewReader(prefix+r+"cd")), "gzip", -1, len(prefix)+1)
			require.NoError(t, err)
			assert.Equal(t, prefix, snip)
		})
	}

	t.Run("exact boundary", func(t *testing.T) {
		for _, r := range multiByteRunes {
			body := prefix + r
			snip, _, err := peek(io.NopCloser(strings.NewReader(body)), "", len(body), len(body))
			require.NoError(t, err)
			assert.Equal(t, body, snip)
		}
	})
}

func TestPeekGzip(t *testing.T) {
	t.Run("bomb fits in the peek window", func(t *testing.T) {
		gz := gzipBytes(t, make([]byte, 4<<20))
		require.Less(t, len(gz), bodyCutoff)
		snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), bodyCutoff)
		require.NoError(t, err)
		assert.Len(t, snip, bodyCutoff)
	})

	t.Run("bomb exceeds the peek window", func(t *testing.T) {
		gz := gzipBytes(t, make([]byte, 8<<20))
		require.Greater(t, len(gz), bodyCutoff)
		snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), bodyCutoff)
		require.NoError(t, err)
		assert.Len(t, snip, bodyCutoff)
	})

	t.Run("encoding casing is ignored", func(t *testing.T) {
		// Content-Encoding is a case-insensitive HTTP token (RFC 9110 8.4.1).
		body := `{"error":{"type":"index_not_found_exception"}}`
		snip, _, err := peek(io.NopCloser(bytes.NewReader(gzipBytes(t, []byte(body)))), "GZip", -1, bodyCutoff)
		require.NoError(t, err)
		assert.Equal(t, body, snip)
	})
}

// perform sends a request through a traced client to a server that answers with status, headers
// and respBody, and returns the single finished span.
func perform(t *testing.T, req *http.Request, status int, header http.Header, respBody []byte) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		maps.Copy(w.Header(), header)
		w.WriteHeader(status)
		_, _ = w.Write(respBody)
	}))
	defer srv.Close()

	client, err := NewClient(opensearch.Config{Addresses: []string{srv.URL}})
	require.NoError(t, err)
	resp, err := client.Perform(req)
	require.NoError(t, err)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, respBody, got, "the response body must be unchanged")

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}

// TestPerformUTF8BodyCutoff checks that the opensearch.body and error.message tags stay valid
// UTF-8 when a multi-byte rune straddles bodyCutoff.
func TestPerformUTF8BodyCutoff(t *testing.T) {
	for _, r := range multiByteRunes {
		for k := 0; k <= len(r); k++ {
			t.Run(strconv.Itoa(len(r))+"-byte/"+strconv.Itoa(k), func(t *testing.T) {
				body := strings.Repeat("a", bodyCutoff-k) + r + strings.Repeat("b", 64)
				want := bodyCutoff - k
				if k == len(r) {
					want = bodyCutoff
				}
				req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/twitter/_search", strings.NewReader(body))
				require.NoError(t, err)
				span := perform(t, req, http.StatusBadRequest, nil, []byte(body))
				for _, key := range []string{ext.OpenSearchBody, ext.ErrorMsg} {
					tag := span.Tag(key).(string)
					assert.True(t, utf8.ValidString(tag), "%s ends in %q", key, tag[max(0, len(tag)-4):])
					assert.Len(t, tag, want, key)
					assert.True(t, strings.HasPrefix(body, tag), key)
				}
			})
		}
	}
}

// TestPerformGzipErrorResponse is a regression test: the error response is decoded using its own
// Content-Encoding, not the request's, and the decoded snippet is bounded by bodyCutoff.
func TestPerformGzipErrorResponse(t *testing.T) {
	gzipHeader := http.Header{"Content-Encoding": {"gzip"}}

	t.Run("uncompressed request", func(t *testing.T) {
		errBody := `{"error":{"type":"index_not_found_exception"}}`
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/twitter/_search", nil)
		require.NoError(t, err)
		// Set explicitly: otherwise net/http requests gzip on its own, transparently
		// inflates the response, and strips Content-Encoding before peek() sees it.
		req.Header.Set("Accept-Encoding", "gzip")
		span := perform(t, req, http.StatusNotFound, gzipHeader, gzipBytes(t, []byte(errBody)))
		assert.Equal(t, errBody, span.Tag(ext.ErrorMsg))
	})

	t.Run("gzip request, uncompressed response", func(t *testing.T) {
		reqBody := `{"query":{"match_all":{}}}`
		errBody := `{"error":{"type":"index_not_found_exception"}}`
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/twitter/_search", bytes.NewReader(gzipBytes(t, []byte(reqBody))))
		require.NoError(t, err)
		req.Header.Set("Content-Encoding", "gzip")
		span := perform(t, req, http.StatusNotFound, nil, []byte(errBody))
		assert.Equal(t, reqBody, span.Tag(ext.OpenSearchBody))
		assert.Equal(t, errBody, span.Tag(ext.ErrorMsg))
	})

	t.Run("bomb", func(t *testing.T) {
		bomb := gzipBytes(t, make([]byte, 4<<20))
		require.Less(t, len(bomb), bodyCutoff)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/twitter/_search", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")
		header := http.Header{"Content-Encoding": {"gzip"}, "Content-Length": {strconv.Itoa(len(bomb))}}
		span := perform(t, req, http.StatusInternalServerError, header, bomb)
		assert.Len(t, span.Tag(ext.ErrorMsg).(string), bodyCutoff)
	})
}
