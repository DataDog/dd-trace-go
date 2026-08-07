// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const debug = false

const (
	elasticV6URL = "http://127.0.0.1:9202"
	elasticV7URL = "http://127.0.0.1:9203"
	elasticV8URL = "http://127.0.0.1:9204"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestQuantize(t *testing.T) {
	for _, tc := range []struct {
		url, method string
		expected    string
	}{
		{
			url:      "/twitter/tweets",
			method:   "POST",
			expected: "POST /twitter/tweets",
		},
		{
			url:      "/logs_2016_05/event/_search",
			method:   "GET",
			expected: "GET /logs_?_?/event/_search",
		},
		{
			url:      "/twitter/tweets/123",
			method:   "GET",
			expected: "GET /twitter/tweets/?",
		},
		{
			url:      "/logs_2016_05/event/123",
			method:   "PUT",
			expected: "PUT /logs_?_?/event/?",
		},
	} {
		assert.Equal(t, tc.expected, quantize(tc.url, tc.method))
	}
}

func TestPeek(t *testing.T) {
	assert := assert.New(t)

	for _, tt := range [...]struct {
		max  int    // content length
		txt  string // stream
		n    int    // bytes to peek at
		snip string // expected snippet
		err  error  // expected error
	}{
		0: {
			// extract 3 bytes from a content of length 7
			txt:  "ABCDEFG",
			max:  7,
			n:    3,
			snip: "ABC",
		},
		1: {
			// extract 7 bytes from a content of length 7
			txt:  "ABCDEFG",
			max:  7,
			n:    7,
			snip: "ABCDEFG",
		},
		2: {
			// extract 100 bytes from a content of length 9 (impossible scenario)
			txt:  "ABCDEFG",
			max:  9,
			n:    100,
			snip: "ABCDEFG",
		},
		3: {
			// extract 5 bytes from a content of length 2 (impossible scenario)
			txt:  "ABCDEFG",
			max:  2,
			n:    5,
			snip: "AB",
		},
		4: {
			txt:  "ABCDEFG",
			max:  0,
			n:    1,
			snip: "A",
		},
		5: {
			n:   4,
			max: 4,
			err: errors.New("empty stream"),
		},
		6: {
			txt:  "ABCDEFG",
			n:    4,
			max:  -1,
			snip: "ABCD",
		},
	} {
		var readcloser io.ReadCloser
		if tt.txt != "" {
			readcloser = io.NopCloser(bytes.NewBufferString(tt.txt))
		}
		snip, rc, err := peek(readcloser, "", tt.max, tt.n)
		assert.Equal(tt.err, err)
		assert.Equal(tt.snip, snip)

		if readcloser != nil {
			// if a non-nil io.ReadCloser was sent, the returned io.ReadCloser
			// must always return the entire original content.
			all, err := io.ReadAll(rc)
			assert.Nil(err)
			assert.Equal(tt.txt, string(all))
		}
	}
}

// gzipped returns b compressed as a single gzip member.
func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	require.NoError(t, err)
	_, err = zw.Write(b)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestPeekGzip(t *testing.T) {
	t.Run("bomb fits in the peek window", func(t *testing.T) {
		gz := gzipped(t, make([]byte, 4<<20))
		require.Less(t, len(gz), bodyCutoff)

		snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), bodyCutoff)
		require.NoError(t, err)
		assert.Len(t, snip, bodyCutoff)
	})

	t.Run("bomb exceeds the peek window", func(t *testing.T) {
		gz := gzipped(t, make([]byte, 8<<20))
		require.Greater(t, len(gz), bodyCutoff)

		snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), bodyCutoff)
		require.NoError(t, err)
		assert.Len(t, snip, bodyCutoff)
	})

	t.Run("small body decodes in full", func(t *testing.T) {
		body := `{"error":{"type":"index_not_found_exception"}}`
		gz := gzipped(t, []byte(body))

		snip, _, err := peek(io.NopCloser(bytes.NewReader(gz)), "gzip", len(gz), bodyCutoff)
		require.NoError(t, err)
		assert.Equal(t, body, snip)
	})
}

func TestRoundTripperGzipBodyCutoff(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	bomb := gzipped(t, make([]byte, 4<<20))
	require.Less(t, len(bomb), bodyCutoff)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(bomb)))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(bomb)
	}))
	defer srv.Close()

	reqBody := `{"query":{"match_all":{}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/twitter/_search", bytes.NewReader(gzipped(t, []byte(reqBody))))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")
	// Set explicitly: otherwise net/http adds it itself, transparently inflates the
	// response, and strips Content-Encoding before peek() ever sees a gzip stream.
	req.Header.Set("Accept-Encoding", "gzip")

	res, err := (&http.Client{Transport: NewRoundTripper()}).Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	span := mt.FinishedSpans()[0]
	assert.Equal(t, reqBody, span.Tag("elasticsearch.body"))
	assert.Len(t, span.Tag(ext.ErrorMsg).(string), bodyCutoff)
}

// TestRoundTripperGzipErrorResponseUncompressedRequest is a regression test: peek() must use
// each body's own Content-Encoding header. A gzip-compressed error response must be decoded
// even when the request itself was not gzip-compressed.
func TestRoundTripperGzipErrorResponseUncompressedRequest(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	errBody := `{"error":{"type":"index_not_found_exception"}}`
	gz := gzipped(t, []byte(errBody))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(gz)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/twitter/_search", nil)
	require.NoError(t, err)
	// Set explicitly: otherwise net/http requests gzip on its own, transparently
	// inflates the response, and strips Content-Encoding before peek() sees it.
	req.Header.Set("Accept-Encoding", "gzip")

	res, err := (&http.Client{Transport: NewRoundTripper()}).Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	span := mt.FinishedSpans()[0]
	assert.Equal(t, errBody, span.Tag(ext.ErrorMsg))
}

// TestRoundTripperGzipBodyCutoffUncompressedRequest covers the path TestRoundTripperGzipBodyCutoff
// doesn't: a gzip-bomb response arriving on an uncompressed request. Content-Encoding is now read
// from the response's own header, so this path decodes gzip where it previously wouldn't have; it
// must still respect bodyCutoff.
func TestRoundTripperGzipBodyCutoffUncompressedRequest(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	bomb := gzipped(t, make([]byte, 4<<20))
	require.Less(t, len(bomb), bodyCutoff)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(bomb)))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(bomb)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/twitter/_search", nil)
	require.NoError(t, err)
	// Set explicitly: otherwise net/http requests gzip on its own, transparently
	// inflates the response, and strips Content-Encoding before peek() sees it.
	req.Header.Set("Accept-Encoding", "gzip")

	res, err := (&http.Client{Transport: NewRoundTripper()}).Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	span := mt.FinishedSpans()[0]
	assert.Len(t, span.Tag(ext.ErrorMsg).(string), bodyCutoff)
}
