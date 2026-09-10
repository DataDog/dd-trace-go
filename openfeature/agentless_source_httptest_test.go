// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

// fakeUFCBackendETag is the ETag every "valid" response carries, mirroring
// system-tests' utils/mocked_backend/ffe.py UFC_ETAG.
const fakeUFCBackendETag = `"ufc-v1"`

// fakeUFCBackendMalformedETag is the ETag a "malformed_with_etag" response
// carries. It must differ from fakeUFCBackendETag so a test can assert that
// the source's held ETag did not advance to it.
const fakeUFCBackendMalformedETag = `"ufc-malformed"`

const fakeUFCValidBody = `{
	"data": {
		"id": "1",
		"type": "universal-flag-configuration",
		"attributes": {
			"createdAt": "2024-04-17T19:40:53.716Z",
			"format": "SERVER",
			"environment": {"name": "Test"},
			"flags": {}
		}
	}
}`

// fakeUFCBackend is an httptest.Server-backed fake of the Agentless
// configuration endpoint, mirroring the response ids of system-tests'
// utils/mocked_backend/ffe.py so the two suites assert the same contract.
type fakeUFCBackend struct {
	server *httptest.Server

	mu              sync.Mutex
	responses       []string // next response id; the last one repeats
	requestsTotal   int
	inFlight        int
	maxInFlight     int
	lastIfNoneMatch string
	lastAuthPresent bool
}

func newFakeUFCBackend(t *testing.T) *fakeUFCBackend {
	t.Helper()
	b := &fakeUFCBackend{responses: []string{"valid"}}
	b.server = httptest.NewServer(http.HandlerFunc(b.handle))
	t.Cleanup(b.server.Close)
	return b
}

func (b *fakeUFCBackend) setResponses(ids ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.responses = ids
}

func (b *fakeUFCBackend) status() (requestsTotal, maxInFlight int, lastIfNoneMatch string, lastAuthPresent bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.requestsTotal, b.maxInFlight, b.lastIfNoneMatch, b.lastAuthPresent
}

func (b *fakeUFCBackend) handle(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.requestsTotal++
	b.inFlight++
	if b.inFlight > b.maxInFlight {
		b.maxInFlight = b.inFlight
	}
	b.lastIfNoneMatch = r.Header.Get("If-None-Match")
	b.lastAuthPresent = r.Header.Get("DD-API-KEY") != ""
	response := b.responses[0]
	if len(b.responses) > 1 {
		b.responses = b.responses[1:]
	}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}()

	switch response {
	case "unauthorized":
		w.WriteHeader(http.StatusUnauthorized)
	case "malformed":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"flags": [`))
	case "malformed_with_etag":
		// Carries its own distinct ETag so a test can tell "the held ETag did
		// not advance to this response's ETag" apart from "there was no ETag to
		// advance to".
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", fakeUFCBackendMalformedETag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"flags": [`))
	case "not_modified":
		w.Header().Set("ETag", fakeUFCBackendETag)
		w.WriteHeader(http.StatusNotModified)
	case "server_error":
		w.WriteHeader(http.StatusInternalServerError)
	case "not_found":
		// A non-retryable status that is neither 401 nor 403, so it reaches the
		// "unexpected status" branch rather than the authentication one.
		w.WriteHeader(http.StatusNotFound)
	case "throttled":
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	case "valid_no_etag":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeUFCValidBody))
	case "delayed_valid":
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", fakeUFCBackendETag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeUFCValidBody))
	case "gzip_valid":
		var buf strings.Builder
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte(fakeUFCValidBody))
		_ = gz.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", fakeUFCBackendETag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(buf.String()))
	default: // "valid"
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", fakeUFCBackendETag)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeUFCValidBody))
	}
}

// newAgentlessSourceOnly builds a source pointed at backend's origin, with
// retries sped up (no real waiting), but never calls start(). Stop on a
// source whose run loop never started would otherwise wait out its full
// context timeout in cleanup, since doneCh would never close: this leaves
// lifecycle to the caller instead, for tests that drive pollOnce directly.
func newAgentlessSourceOnly(t *testing.T, backend *fakeUFCBackend, pollInterval time.Duration, apply func(*universalFlagsConfiguration)) *agentlessSource {
	t.Helper()
	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     pollInterval,
		RequestTimeout:   2 * time.Second,
	}, apply)
	require.NoError(t, err)
	src.retryDelay = func(int) time.Duration { return 0 }
	return src
}

// newTestAgentlessSource builds a source pointed at backend's origin, with
// retries sped up (no real waiting) unless the test needs otherwise, and
// starts its poll loop.
func newTestAgentlessSource(t *testing.T, backend *fakeUFCBackend, pollInterval time.Duration, apply func(*universalFlagsConfiguration)) *agentlessSource {
	t.Helper()
	src := newAgentlessSourceOnly(t, backend, pollInterval, apply)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		src.Stop(ctx)
	})
	return src
}

func TestAgentlessSource_StartDoesNotBlock(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("delayed_valid") // 150ms handler sleep
	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {})

	start := time.Now()
	src.start()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 50*time.Millisecond, "start must not block on the first poll")

	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 1
	}, 2*time.Second, time.Millisecond, "the first poll must still happen, just asynchronously")
}

// TestAgentlessSource_ETagNotAdvancedOnParseFailure drives pollOnce directly,
// rather than start()+Eventually on a request count: the previous version
// waited for requestsTotal >= 3, then read the backend's single shared
// lastIfNoneMatch field, which a fourth request (the loop's next tick) could
// overwrite before the assertion ran, and a request count alone cannot
// distinguish a request that arrived from one whose response was already
// processed. Calling pollOnce synchronously removes both races: each call
// only returns once its request has both arrived and been fully handled, and
// each backend.status() snapshot is read for the specific request that
// produced it, not for whatever request happens to be latest.
func TestAgentlessSource_ETagNotAdvancedOnParseFailure(t *testing.T) {
	backend := newFakeUFCBackend(t)
	// The malformed response carries its own distinct ETag: this test's
	// contract is that a malformed body never advances the held ETag to a new
	// value, and a malformed response with no ETag at all could not tell that
	// apart from correctly not advancing.
	backend.setResponses("valid", "malformed_with_etag", "not_modified")

	var mu sync.Mutex
	var applied []*universalFlagsConfiguration
	src := newAgentlessSourceOnly(t, backend, time.Hour, func(c *universalFlagsConfiguration) {
		mu.Lock()
		defer mu.Unlock()
		applied = append(applied, c)
	})

	outer, _ := src.pollOnce() // valid: applies configuration and stores the ETag
	require.Equal(t, pollOutcomeSuccess, outer)
	assert.Equal(t, fakeUFCBackendETag, src.currentETag())

	outer, _ = src.pollOnce() // malformed_with_etag: must not advance the ETag
	require.Equal(t, pollOutcomeStop, outer)
	assert.Equal(t, fakeUFCBackendETag, src.currentETag(), "a malformed payload must never be acknowledged as received")

	_, _, lastIfNoneMatch, _ := backend.status()
	assert.Equal(t, fakeUFCBackendETag, lastIfNoneMatch, "the third request must still carry the ETag from the first, valid response")

	outer, _ = src.pollOnce() // not_modified: confirms the server still recognizes that ETag
	require.Equal(t, pollOutcomeSuccess, outer)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, applied, 1, "only the first, valid poll should have applied a configuration")
}

// TestAgentlessSource_ETagAnd304 drives pollOnce directly instead of
// start()+Eventually: waiting for requestsTotal >= 1 only proves a request
// arrived, not that its response was processed, and a second request (the
// loop's next tick) could overwrite the backend's shared lastIfNoneMatch
// field before either assertion ran.
func TestAgentlessSource_ETagAnd304(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid", "not_modified")

	src := newAgentlessSourceOnly(t, backend, time.Hour, func(*universalFlagsConfiguration) {})

	outer, _ := src.pollOnce()
	require.Equal(t, pollOutcomeSuccess, outer)
	_, _, lastIfNoneMatch, _ := backend.status()
	assert.Empty(t, lastIfNoneMatch, "the first request must not carry an ETag")

	outer, _ = src.pollOnce()
	require.Equal(t, pollOutcomeSuccess, outer)
	_, _, lastIfNoneMatch, _ = backend.status()
	assert.Equal(t, fakeUFCBackendETag, lastIfNoneMatch, "the second request must carry the ETag stored from the first")
}

// TestAgentlessSource_BlankETagClears drives pollOnce directly instead of
// start()+Eventually on a request count, for the same reason as the two
// tests above: requestsTotal >= 3 only proves the third request arrived, and
// a fourth request could overwrite the shared lastIfNoneMatch field before
// the assertion ran.
func TestAgentlessSource_BlankETagClears(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid", "valid_no_etag", "valid")

	src := newAgentlessSourceOnly(t, backend, time.Hour, func(*universalFlagsConfiguration) {})

	outer, _ := src.pollOnce() // valid: stores the ETag
	require.Equal(t, pollOutcomeSuccess, outer)
	assert.Equal(t, fakeUFCBackendETag, src.currentETag())

	outer, _ = src.pollOnce() // valid_no_etag: must clear the held ETag
	require.Equal(t, pollOutcomeSuccess, outer)
	assert.Empty(t, src.currentETag(), "a blank ETag response must clear the held ETag")

	outer, _ = src.pollOnce() // valid: confirms the cleared ETag reached the request
	require.Equal(t, pollOutcomeSuccess, outer)
	_, _, lastIfNoneMatch, _ := backend.status()
	assert.Empty(t, lastIfNoneMatch, "the third request must not carry an ETag, since the second response cleared it")
}

func TestAgentlessSource_RetryWithinPoll(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("server_error", "server_error", "valid")

	var mu sync.Mutex
	var applied int
	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {
		mu.Lock()
		defer mu.Unlock()
		applied++
	})

	src.start()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return applied == 1
	}, 2*time.Second, time.Millisecond)

	requests, _, _, _ := backend.status()
	assert.Equal(t, 3, requests, "exactly 3 requests within the first poll")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, applied)
}

func TestAgentlessSource_RetriesExhausted(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("server_error", "server_error", "server_error")

	var mu sync.Mutex
	var applied int
	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {
		mu.Lock()
		defer mu.Unlock()
		applied++
	})

	src.start()

	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 3
	}, 2*time.Second, time.Millisecond)

	requests, _, _, _ := backend.status()
	assert.Equal(t, 3, requests)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 0, applied)
}

func TestAgentlessSource_PollOnceReturnsRetryAfter(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("throttled")

	// This source's run loop never starts, so it must use newAgentlessSourceOnly,
	// not newTestAgentlessSource: the latter's cleanup calls Stop, which would
	// wait out its full context timeout for a doneCh that a never-started run
	// loop can never close.
	src := newAgentlessSourceOnly(t, backend, time.Hour, func(*universalFlagsConfiguration) {})
	outcome, retryAfter := src.pollOnce()
	assert.Equal(t, pollOutcomeRetryable, outcome)
	assert.Equal(t, 2*time.Second, retryAfter)
}

func TestAgentlessSource_NoWarningOnGracefulShutdownCancel(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("delayed_valid") // 150ms handler sleep

	logger, undo := newCapturingLogger()
	defer undo()

	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     time.Hour,
		RequestTimeout:   5 * time.Second,
	}, func(*universalFlagsConfiguration) {})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		src.start()
		close(done)
	}()

	time.Sleep(20 * time.Millisecond) // let the request start before stopping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	src.Stop(ctx)
	<-done

	assert.Equal(t, 0, logger.countContaining("request failed"), "a Stop-triggered cancellation must not warn")
}

func TestAgentlessSource_NonRetryableStopsImmediately(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("unauthorized")

	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {})
	src.start()

	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 1
	}, 2*time.Second, time.Millisecond)

	requests, _, _, _ := backend.status()
	assert.Equal(t, 1, requests, "an auth failure must not be retried")
}

func TestAgentlessSource_LastKnownGood(t *testing.T) {
	for name, secondResponse := range map[string]string{
		"server_error": "server_error",
		"unauthorized": "unauthorized",
		"malformed":    "malformed",
	} {
		t.Run(name, func(t *testing.T) {
			backend := newFakeUFCBackend(t)
			backend.setResponses("valid", secondResponse)

			var mu sync.Mutex
			var applied []*universalFlagsConfiguration
			src := newTestAgentlessSource(t, backend, 5*time.Millisecond, func(c *universalFlagsConfiguration) {
				mu.Lock()
				defer mu.Unlock()
				applied = append(applied, c)
			})
			src.start()

			require.Eventually(t, func() bool {
				requests, _, _, _ := backend.status()
				return requests >= 2
			}, 2*time.Second, time.Millisecond)

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, applied, 1, "the failing poll must not apply a second configuration")
		})
	}
}

func TestAgentlessSource_NoOverlap(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("delayed_valid", "delayed_valid", "delayed_valid", "delayed_valid", "delayed_valid")

	src := newTestAgentlessSource(t, backend, 20*time.Millisecond, func(*universalFlagsConfiguration) {})
	src.start()

	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 3
	}, 5*time.Second, time.Millisecond)

	_, maxInFlight, _, _ := backend.status()
	assert.Equal(t, 1, maxInFlight, "no two polls may be in flight at once")
}

func TestAgentlessSource_DoesNotFollowRedirects(t *testing.T) {
	var mu sync.Mutex
	var backendRequests, redirectTargetRequests int

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		redirectTargetRequests++
		mu.Unlock()
		if r.Header.Get("DD-API-KEY") != "" {
			t.Error("the redirect target must never receive DD-API-KEY")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		backendRequests++
		mu.Unlock()
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer backend.Close()

	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: backend.URL,
		APIKey:           "secret-api-key",
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}, func(*universalFlagsConfiguration) {})
	require.NoError(t, err)

	src.start()

	// Wait for the poll to actually reach the backend before asserting the
	// redirect target was never hit — start() only schedules the poll
	// goroutine and returns immediately, so this must not race it.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return backendRequests >= 1
	}, 2*time.Second, time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 0, redirectTargetRequests, "a redirect must never be followed")
}

func TestSanitizeTransportError_DoesNotLeakSecrets(t *testing.T) {
	const secret = "s3cr3t-token"
	err := &url.Error{
		Op:  "Get",
		URL: "https://user:" + secret + "@example.com/path",
		Err: errors.New("connection refused"),
	}

	got := sanitizeTransportError(err)
	assert.False(t, strings.Contains(got, secret))
	assert.Equal(t, "connection refused", got)
}

func TestAgentlessSource_APIKeyOnlySentToManagedEndpoint(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid")

	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: backend.server.URL, // custom endpoint
		APIKey:           "secret-api-key",
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}, func(*universalFlagsConfiguration) {})
	require.NoError(t, err)
	assert.Empty(t, src.apiKey, "a custom endpoint must never receive the API key")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		src.Stop(ctx)
	})
	src.start()

	// Wait for the poll to actually reach the backend before asserting no
	// Authorization header arrived — start() only schedules the poll goroutine
	// and returns immediately, so lastAuthPresent would otherwise be read on
	// its zero value and pass trivially.
	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 1
	}, 2*time.Second, time.Millisecond)

	_, _, _, lastAuthPresent := backend.status()
	assert.False(t, lastAuthPresent)
}

func TestAgentlessSource_Headers(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid")

	var mu sync.Mutex
	var gotHeaders http.Header
	backend.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHeaders = r.Header.Clone()
		mu.Unlock()
		backend.handle(w, r)
	})

	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {})
	src.start()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gotHeaders != nil
	}, 2*time.Second, time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "gzip", gotHeaders.Get("Accept-Encoding"))
	assert.Equal(t, "go", gotHeaders.Get("DD-Client-Library-Language"))
	assert.NotEmpty(t, gotHeaders.Get("DD-Client-Library-Version"))
}

func TestAgentlessSource_Gzip(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("gzip_valid")

	var mu sync.Mutex
	var applied int
	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {
		mu.Lock()
		defer mu.Unlock()
		applied++
	})
	src.start()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return applied == 1
	}, 2*time.Second, time.Millisecond)
}

func TestAgentlessSource_WarningDedupe(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("unauthorized", "unauthorized", "unauthorized", "unauthorized", "unauthorized")

	logger, undo := newCapturingLogger()
	defer undo()

	src := newTestAgentlessSource(t, backend, 5*time.Millisecond, func(*universalFlagsConfiguration) {})
	src.start()

	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 5
	}, 2*time.Second, time.Millisecond)

	assert.Equal(t, 1, logger.countContaining("authentication"))
}

// TestAgentlessSource_WarningDedupeIsPerCause pins that warnOnce dedupes per
// failure cause, not per broad area. A transient 500 must not spend the warn
// slot of the 404 that follows it: the 404 stops polling, so its warning is
// the only outward sign that the configuration has frozen at last-known-good.
func TestAgentlessSource_WarningDedupeIsPerCause(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("server_error", "not_found")

	logger, undo := newCapturingLogger()
	defer undo()

	src := newTestAgentlessSource(t, backend, time.Hour, func(*universalFlagsConfiguration) {})
	src.start()

	// Wait on the second warning itself, not on the request count: the backend
	// counts a request before the client has processed its response, so
	// asserting on the count would race the log write.
	require.Eventually(t, func() bool {
		return logger.countContaining("unexpected status 404") == 1
	}, 2*time.Second, time.Millisecond)

	assert.Equal(t, 1, logger.countContaining("retryable status 500"))
}

func TestAgentlessSource_NoLogContainsTheEndpoint(t *testing.T) {
	const secret = "s3cr3t-token"

	logger, undo := newCapturingLogger()
	defer undo()

	for _, responses := range [][]string{
		{"unauthorized"},
		{"malformed"},
		{"server_error", "server_error", "server_error"},
	} {
		backend := newFakeUFCBackend(t)
		backend.setResponses(responses...)
		src, err := newAgentlessSource(internalffe.Settings{
			AgentlessBaseURL: backend.server.URL + "/" + secret,
			PollInterval:     time.Hour,
			RequestTimeout:   2 * time.Second,
		}, func(*universalFlagsConfiguration) {})
		require.NoError(t, err)
		src.retryDelay = func(int) time.Duration { return 0 }

		src.start()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		src.Stop(ctx)
		cancel()
	}

	assert.Equal(t, 0, logger.countContaining(secret))
}

// capturingLogger implements log.Logger, recording every message.
type capturingLogger struct {
	mu       sync.Mutex
	messages []string
}

func newCapturingLogger() (*capturingLogger, func()) {
	l := &capturingLogger{}
	undo := log.UseLogger(l)
	return l, undo
}

func (l *capturingLogger) Log(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, msg)
}

func (l *capturingLogger) countContaining(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, msg := range l.messages {
		if strings.Contains(msg, substr) {
			count++
		}
	}
	return count
}
