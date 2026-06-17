// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
)

func TestCIVisibilityImplementsTraceWriter(t *testing.T) {
	assert.Implements(t, (*traceWriter)(nil), &ciVisibilityTraceWriter{})
}

type failingCiVisibilityTransport struct {
	dummyTransport
	failCount     int
	sendAttempts  int
	tracesSent    bool
	bodyOnFailure bool                  // Returns a response body with send errors to verify defensive cleanup.
	events        ciVisibilityEvents    // Records the first payload so retry attempts can verify payload stability.
	assert        *assert.Assertions    // Reports payload mismatches from the writer goroutine.
	bodies        []*trackingReadCloser // Tracks response bodies returned to the writer.
}

func (t *failingCiVisibilityTransport) send(p payload) (io.ReadCloser, error) {
	defer p.Close()
	t.sendAttempts++

	ciVisibilityPayload := &ciVisibilityPayload{payload: p, serializationTime: 0}

	var events ciVisibilityEvents
	err := msgp.Decode(ciVisibilityPayload, &events)
	if err != nil {
		return nil, err
	}
	if t.sendAttempts == 1 {
		t.events = events
	} else {
		t.assert.Equal(t.events, events)
	}

	if t.failCount > 0 {
		t.failCount--
		if t.bodyOnFailure {
			body := newTrackingReadCloser("ERROR")
			t.bodies = append(t.bodies, body)
			return body, errors.New("oops, I failed")
		}
		return nil, errors.New("oops, I failed")
	}

	t.tracesSent = true
	body := newTrackingReadCloser("OK")
	t.bodies = append(t.bodies, body)
	return body, nil
}

func TestCiVisibilityTraceWriterFlushRetries(t *testing.T) {
	testcases := []struct {
		configRetries int
		retryInterval time.Duration
		failCount     int
		bodyOnFailure bool
		tracesSent    bool
		expAttempts   int
	}{
		{configRetries: 0, retryInterval: time.Millisecond, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 0, retryInterval: time.Millisecond, failCount: 1, tracesSent: false, expAttempts: 1},

		{configRetries: 1, retryInterval: time.Millisecond, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 1, bodyOnFailure: true, tracesSent: true, expAttempts: 2},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 2, tracesSent: false, expAttempts: 2},

		{configRetries: 2, retryInterval: time.Millisecond, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 2, tracesSent: true, expAttempts: 3},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 3, tracesSent: false, expAttempts: 3},

		{configRetries: 1, retryInterval: 2 * time.Millisecond, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 2, retryInterval: 2 * time.Millisecond, failCount: 2, tracesSent: true, expAttempts: 3},
	}

	ss := []*Span{makeSpan(0)}
	for _, test := range testcases {
		name := fmt.Sprintf("%d-%d-%t-%t-%d", test.configRetries, test.failCount, test.bodyOnFailure, test.tracesSent, test.expAttempts)
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			p := &failingCiVisibilityTransport{
				failCount:     test.failCount,
				bodyOnFailure: test.bodyOnFailure,
				assert:        assert,
			}
			c, err := newTestConfig(func(c *config) {
				c.ddTransport = p
				c.internalConfig.SetSendRetries(test.configRetries, internalconfig.OriginCode)
				c.internalConfig.SetRetryInterval(test.retryInterval, internalconfig.OriginCode)
			})
			assert.NoError(err)

			h := newCiVisibilityTraceWriter(c)
			h.add(ss)

			start := time.Now()
			h.flush()
			h.wg.Wait()
			elapsed := time.Since(start)

			assert.Equal(test.expAttempts, p.sendAttempts)
			assert.Equal(test.tracesSent, p.tracesSent)
			for _, body := range p.bodies {
				assert.Equal(body.expectedBytes, body.bytesRead)
				assert.True(body.closed)
			}

			if test.configRetries > 0 && test.failCount > 1 {
				assert.GreaterOrEqual(elapsed, test.retryInterval*time.Duration(minInts(test.configRetries+1, test.failCount)))
			}
		})
	}
}

func TestCiVisibilityTraceWriterClosesHTTPResponseBody(t *testing.T) {
	assert := assert.New(t)
	closedConn := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			signalCIVisibilityHTTPConnectionState(closedConn)
		}
	}
	server.Start()
	defer server.Close()

	c, err := newTestConfig(func(c *config) {
		c.httpClient = server.Client()
		c.ddTransport = &ciVisibilityTransport{
			config:           c,
			testCycleURLPath: server.URL,
			headers:          map[string]string{"Content-Type": "application/msgpack"},
			agentless:        false,
		}
		c.internalConfig.SetSendRetries(0, internalconfig.OriginCode)
	})
	assert.NoError(err)

	h := newCiVisibilityTraceWriter(c)
	h.add([]*Span{makeSpan(0)})
	h.flush()
	h.wg.Wait()

	server.Client().CloseIdleConnections()
	waitForCIVisibilityHTTPConnectionState(t, closedConn, "closed")
}

// trackingReadCloser records whether a response body was fully drained and closed.
type trackingReadCloser struct {
	reader        *strings.Reader
	closed        bool
	bytesRead     int
	expectedBytes int
}

// newTrackingReadCloser creates a response body tracker for the given content.
func newTrackingReadCloser(body string) *trackingReadCloser {
	return &trackingReadCloser{
		reader:        strings.NewReader(body),
		expectedBytes: len(body),
	}
}

// Read records how many bytes the writer consumes from the response body.
func (rc *trackingReadCloser) Read(p []byte) (int, error) {
	n, err := rc.reader.Read(p)
	rc.bytesRead += n
	return n, err
}

// Close records that the writer released the response body.
func (rc *trackingReadCloser) Close() error {
	rc.closed = true
	return nil
}

// signalCIVisibilityHTTPConnectionState records a connection state transition
// without blocking the HTTP server connection-state callback.
func signalCIVisibilityHTTPConnectionState(state chan<- struct{}) {
	select {
	case state <- struct{}{}:
	default:
	}
}

// waitForCIVisibilityHTTPConnectionState waits for an expected server
// connection-state transition produced after response-body cleanup.
func waitForCIVisibilityHTTPConnectionState(t *testing.T, state <-chan struct{}, stateName string) {
	t.Helper()
	select {
	case <-state:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for connection to become %s", stateName)
	}
}

func TestCiVisibilityTraceWriterProcessTags(t *testing.T) {
	makeSpans := func(n int) []*Span {
		spans := make([]*Span, n)
		for i := range spans {
			spans[i] = makeSpan(0)
		}
		return spans
	}

	t.Run("enabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "true")
		processtags.Reload()
		t.Cleanup(processtags.Reload)

		captured := &capturingCiTransport{}
		cfg, err := newTestConfig(func(c *config) { c.ddTransport = captured })
		require.NoError(t, err)

		w := newCiVisibilityTraceWriter(cfg)
		w.add(makeSpans(3))
		w.flush()
		w.wg.Wait()

		require.Len(t, captured.batches, 1)
		require.Len(t, captured.batches[0], 3)
		assert.Contains(t, captured.batches[0][0].Content.Meta, keyProcessTags, "first event must carry process tags")
		assert.NotContains(t, captured.batches[0][1].Content.Meta, keyProcessTags, "second event must not carry process tags")
		assert.NotContains(t, captured.batches[0][2].Content.Meta, keyProcessTags, "third event must not carry process tags")
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
		processtags.Reload()
		t.Cleanup(processtags.Reload)

		captured := &capturingCiTransport{}
		cfg, err := newTestConfig(func(c *config) { c.ddTransport = captured })
		require.NoError(t, err)

		w := newCiVisibilityTraceWriter(cfg)
		w.add(makeSpans(2))
		w.flush()
		w.wg.Wait()

		require.Len(t, captured.batches, 1)
		require.Len(t, captured.batches[0], 2)
		for i, e := range captured.batches[0] {
			assert.NotContains(t, e.Content.Meta, keyProcessTags, "event %d must not carry process tags when disabled", i)
		}
	})
}

// capturingCiTransport decodes and captures CI visibility events during send.
type capturingCiTransport struct {
	dummyTransport
	batches []ciVisibilityEvents
}

func (t *capturingCiTransport) send(p payload) (io.ReadCloser, error) {
	defer p.Close()
	cp := &ciVisibilityPayload{payload: p}
	var events ciVisibilityEvents
	if err := msgp.Decode(cp, &events); err != nil {
		return nil, err
	}
	t.batches = append(t.batches, events)
	return io.NopCloser(strings.NewReader("")), nil
}
