// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCDNPollerIntervalAndNoOverlap(t *testing.T) {
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var requests atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			max := maxInFlight.Load()
			if current <= max || maxInFlight.CompareAndSwap(max, current) {
				break
			}
		}

		if requests.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}

		require.Equal(t, defaultCDNConfigPath, r.URL.Path)
		w.Header().Set("ETag", `"ufc-no-overlap"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)

	recorder := newPollerApplyRecorder()
	ticks := make(chan time.Time, 4)
	poller := newTestCDNPoller(t, srv, recorder.apply, func(int) time.Duration { return 0 })
	poller.tickC = ticks
	poller.start()
	t.Cleanup(func() { stopPollerForTest(t, poller) })

	require.Eventually(t, func() bool {
		select {
		case <-firstStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	ticks <- time.Now()
	ticks <- time.Now()
	require.EqualValues(t, 1, requests.Load())
	require.EqualValues(t, 1, maxInFlight.Load())

	close(releaseFirst)
	recorder.waitForCount(t, 1)
	ticks <- time.Now()
	require.Eventually(t, func() bool {
		return requests.Load() >= 2
	}, time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, maxInFlight.Load())
}

func TestCDNPollerRetryBackoffAndTimeout(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			<-r.Context().Done()
			return
		}
		w.Header().Set("ETag", `"ufc-after-retry"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)

	var backoffCalls atomic.Int32
	recorder := newPollerApplyRecorder()
	poller := newTestCDNPoller(t, srv, recorder.apply, func(int) time.Duration {
		backoffCalls.Add(1)
		return 0
	})
	poller.requestTimeout = 10 * time.Millisecond

	require.NoError(t, poller.pollOnce(context.Background()))
	require.EqualValues(t, 3, attempts.Load())
	require.EqualValues(t, 2, backoffCalls.Load())
	recorder.requireCount(t, 1)
}

func TestCDNPollerETag304DoesNotReapply(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			w.Header().Set("ETag", `"ufc-v1"`)
			_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
		case 2:
			require.Equal(t, `"ufc-v1"`, r.Header.Get("If-None-Match"))
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected request %d", requests.Load())
		}
	}))
	t.Cleanup(srv.Close)

	recorder := newPollerApplyRecorder()
	poller := newTestCDNPoller(t, srv, recorder.apply, func(int) time.Duration { return 0 })

	require.NoError(t, poller.pollOnce(context.Background()))
	require.NoError(t, poller.pollOnce(context.Background()))
	recorder.requireCount(t, 1)
	require.EqualValues(t, 2, requests.Load())
}

func TestCDNPollerPreservesLastKnownGoodOnFailure(t *testing.T) {
	var serveMalformed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if serveMalformed.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"format":"SERVER","flags":{"broken":`))
			return
		}
		w.Header().Set("ETag", `"ufc-valid"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)

	provider := newDatadogProvider(ProviderConfig{})
	poller := newTestCDNPoller(t, srv, provider.updateConfiguration, func(int) time.Duration { return 0 })

	require.NoError(t, poller.pollOnce(context.Background()))
	require.NotNil(t, provider.getConfiguration())
	serveMalformed.Store(true)
	require.Error(t, poller.pollOnce(context.Background()))
	require.NotNil(t, provider.getConfiguration())
}

func TestCDNPollerShutdownCancelsInFlight(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(srv.Close)

	recorder := newPollerApplyRecorder()
	poller := newTestCDNPoller(t, srv, recorder.apply, func(int) time.Duration { return 0 })
	poller.requestTimeout = time.Minute
	poller.pollInterval = time.Hour
	poller.start()

	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	stopPollerForTest(t, poller)
	require.Eventually(t, func() bool {
		select {
		case <-cancelled:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestCDNPollerMissingAuthFailsClosed(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Empty(t, r.Header.Get(cdnAPIKeyHeader))
		require.Equal(t, string(FeatureFlagSourceModeCDN), r.Header.Get(cdnSourceModeHeader))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	recorder := newPollerApplyRecorder()
	poller := newTestCDNPoller(t, srv, recorder.apply, func(int) time.Duration { return 0 })
	poller.apiKey = ""

	require.Error(t, poller.pollOnce(context.Background()))
	recorder.requireCount(t, 0)
	require.EqualValues(t, 1, requests.Load())
}

func TestCDNPollerRejectsNonLocalHTTPBaseURL(t *testing.T) {
	_, err := buildCDNConfigEndpoint("http://feature-flags.datadoghq.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must use https")

	endpoint, err := buildCDNConfigEndpoint("http://host.docker.internal:4900")
	require.NoError(t, err)
	require.Contains(t, endpoint, defaultCDNConfigPath)
}

type pollerApplyRecorder struct {
	mu      sync.Mutex
	configs []*universalFlagsConfiguration
	applied chan struct{}
}

func newPollerApplyRecorder() *pollerApplyRecorder {
	return &pollerApplyRecorder{applied: make(chan struct{}, 16)}
}

func (r *pollerApplyRecorder) apply(config *universalFlagsConfiguration) {
	r.mu.Lock()
	r.configs = append(r.configs, config)
	r.mu.Unlock()
	r.applied <- struct{}{}
}

func (r *pollerApplyRecorder) waitForCount(t *testing.T, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.configs) >= want
	}, time.Second, 10*time.Millisecond)
}

func (r *pollerApplyRecorder) requireCount(t *testing.T, want int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.configs, want)
}

func newTestCDNPoller(t *testing.T, srv *httptest.Server, apply func(*universalFlagsConfiguration), backoff func(int) time.Duration) *cdnPoller {
	t.Helper()
	poller, err := newCDNPoller(cdnPollerConfig{
		baseURL:        srv.URL,
		apiKey:         "test-api-key",
		pollInterval:   time.Hour,
		requestTimeout: time.Second,
		httpClient:     srv.Client(),
		apply:          apply,
		backoff:        backoff,
	})
	require.NoError(t, err)
	return poller
}

func stopPollerForTest(t *testing.T, poller *cdnPoller) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, poller.stop(ctx))
}
