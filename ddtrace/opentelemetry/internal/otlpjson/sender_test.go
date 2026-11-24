// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otlpjson

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func newTestSender(url string, mutate ...func(*SenderConfig)) *Sender {
	cfg := SenderConfig{URL: url, Timeout: 5 * time.Second, Signal: "logs", RejectedField: "rejectedLogRecords"}
	for _, m := range mutate {
		m(&cfg)
	}
	s := NewSender(cfg)
	// Keep retries fast: tests only care about how many happen.
	s.initialInterval = time.Millisecond
	s.maxInterval = 5 * time.Millisecond
	return s
}

func TestSenderPostsJSONWithHeaders(t *testing.T) {
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()

	s := newTestSender(srv.URL+"/v1/logs", func(c *SenderConfig) { c.Headers = map[string]string{"dd-api-key": "k"} })
	require.NoError(t, s.Send(context.Background(), []byte(`{"a":1}`)))

	assert.Equal(t, http.MethodPost, got.Method)
	assert.Equal(t, "/v1/logs", got.URL.Path)
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
	assert.Equal(t, "k", got.Header.Get("dd-api-key"))
	assert.True(t, strings.HasPrefix(got.Header.Get("User-Agent"), "dd-trace-go/"))
	assert.Empty(t, got.Header.Get("Content-Encoding"))
	assert.Equal(t, `{"a":1}`, body)
}

func TestSenderGzip(t *testing.T) {
	var encoding, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding = r.Header.Get("Content-Encoding")
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not gzip: %v", err)
			return
		}
		b, _ := io.ReadAll(zr)
		body = string(b)
	}))
	defer srv.Close()

	s := newTestSender(srv.URL, func(c *SenderConfig) { c.Gzip = true })
	require.NoError(t, s.Send(context.Background(), []byte(`{"a":1}`)))

	assert.Equal(t, "gzip", encoding)
	assert.Equal(t, `{"a":1}`, body)
}

func TestSenderRetriesRetryableStatuses(t *testing.T) {
	for _, status := range []int{429, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
				}
			}))
			defer srv.Close()

			require.NoError(t, newTestSender(srv.URL).Send(context.Background(), []byte(`{}`)))
			assert.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestSenderDoesNotRetryOtherErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad payload"))
	}))
	defer srv.Close()

	err := newTestSender(srv.URL).Send(context.Background(), []byte(`{}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "bad payload")
	assert.EqualValues(t, 1, calls.Load())
}

// countingTransport counts the connection attempts made through the wrapped transport.
type countingTransport struct {
	http.RoundTripper
	attempts atomic.Int32
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.attempts.Add(1)
	return c.RoundTripper.RoundTrip(r)
}

func TestSenderDoesNotRetryRefusedConnection(t *testing.T) {
	// Grab a free port, then close the listener so nothing accepts on it.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	s := newTestSender(url)
	transport := &countingTransport{RoundTripper: s.client.Transport}
	s.client.Transport = transport

	err := s.Send(context.Background(), []byte(`{}`))

	require.Error(t, err)
	assert.EqualValues(t, 1, transport.attempts.Load())
	assert.NotContains(t, err.Error(), "giving up")
}

func TestSenderHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()

	start := time.Now()
	require.NoError(t, newTestSender(srv.URL).Send(context.Background(), []byte(`{}`)))

	assert.GreaterOrEqual(t, time.Since(start), time.Second, "Retry-After must override the 1ms backoff")
}

func TestSenderGivesUpAfterMaxElapsedTime(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := newTestSender(srv.URL)
	s.maxElapsedTime = 50 * time.Millisecond
	err := s.Send(context.Background(), []byte(`{}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.Greater(t, calls.Load(), int32(1))
}

func TestSenderTimeoutBoundsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := newTestSender(srv.URL, func(c *SenderConfig) { c.Timeout = 100 * time.Millisecond })
	s.initialInterval, s.maxInterval, s.maxElapsedTime = time.Hour, time.Hour, 24*time.Hour

	start := time.Now()
	err := s.Send(context.Background(), []byte(`{}`))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestSenderStopsWhenContextIsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	s := newTestSender(srv.URL)
	s.initialInterval, s.maxInterval, s.maxElapsedTime = time.Hour, time.Hour, 24*time.Hour
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	err := s.Send(ctx, []byte(`{}`))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSenderWarnsOnPartialSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"partialSuccess":{"rejectedLogRecords":"3","errorMessage":"too old"}}`))
	}))
	defer srv.Close()
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()

	require.NoError(t, newTestSender(srv.URL).Send(context.Background(), []byte(`{}`)))

	out := strings.Join(logger.Logs(), "\n")
	assert.Contains(t, out, "partially rejected")
	assert.Contains(t, out, "too old")
}

func TestSenderIgnoresEmptyOrInvalidResponseBodies(t *testing.T) {
	for _, body := range []string{``, `not json`, `{}`, `{"partialSuccess":{}}`, `{"partialSuccess":{"rejectedLogRecords":"0"}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		logger := new(log.RecordLogger)
		restore := log.UseLogger(logger)

		require.NoError(t, newTestSender(srv.URL).Send(context.Background(), []byte(`{}`)), body)

		restore()
		srv.Close()
		assert.NotContains(t, strings.Join(logger.Logs(), "\n"), "partially rejected", body)
	}
}

func TestEncoderEscapesStrings(t *testing.T) {
	e := encoder{}
	e.str("a\"b\\c\n\r\t\x01\x1f")
	assert.Equal(t, `"a\"b\\c\n\r\t\u0001\u001f"`, string(e.buf))
}

func TestEncoderFloats(t *testing.T) {
	var e encoder
	e.beginArray()
	for _, f := range []float64{1.5, 0, 1e21} {
		e.elem()
		e.float(f)
	}
	e.endArray()
	assert.Equal(t, `[1.5,0,1e+21]`, string(e.buf))
}
