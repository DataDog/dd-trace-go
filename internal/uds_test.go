// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnixDataSocketURL(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected *url.URL
	}{
		{
			name: "empty-path",
			path: "",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_",
			},
		},
		{
			name: "no-special-chars",
			path: "path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path",
			},
		},
		{
			name: "with-colon",
			path: "path:with:colons",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_colons",
			},
		},
		{
			name: "with-forward-slash",
			path: "path/with/slashes",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_slashes",
			},
		},
		{
			name: "with-backward-slash",
			path: `path\with\backslashes`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_backslashes",
			},
		},
		{
			name: "mixed-special-chars",
			path: `path:with/all\chars`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_all_chars",
			},
		},
		{
			name: "leading-special-char-colon",
			path: ":path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS__path",
			},
		},
		{
			name: "trailing-special-char-colon",
			path: "path:",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_",
			},
		},
		{
			name: "leading-special-char-slash",
			path: "/path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS__path",
			},
		},
		{
			name: "trailing-special-char-slash",
			path: "path/",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_",
			},
		},
		{
			name: "leading-special-char-backslash",
			path: `\path`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS__path",
			},
		},
		{
			name: "trailing-special-char-backslash",
			path: `path\`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_",
			},
		},
		{
			name: "multiple-leading-special-chars",
			path: "://path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS____path",
			},
		},
		{
			name: "multiple-trailing-special-chars",
			path: "path://",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path___",
			},
		},
		{
			name: "all-special-chars",
			path: `:/\`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS____",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, UnixDataSocketURL(tt.path))
		})
	}
}

func TestUDSClientTransportConfig(t *testing.T) {
	client := UDSClient("/var/run/datadog/apm.socket", 10*time.Second)
	tr, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")
	assert.Equal(t, 100, tr.MaxIdleConns)
	assert.Equal(t, 100, tr.MaxIdleConnsPerHost)
	assert.Equal(t, 90*time.Second, tr.IdleConnTimeout)
	assert.Equal(t, 10*time.Second, tr.TLSHandshakeTimeout)
	assert.Equal(t, 1*time.Second, tr.ExpectContinueTimeout)
}

// TestUDSConcurrentConnectionReuse verifies that MaxIdleConnsPerHost=100 prevents
// connection churn when many goroutines send requests concurrently over a UDS socket.
// Before the fix, MaxIdleConnsPerHost defaulted to 2, which forced new connections
// for every request beyond the 2-connection idle pool, causing "connection reset by
// peer" errors under agent backpressure.
func TestUDSConcurrentConnectionReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping connection pool test in short mode")
	}

	dir, err := os.MkdirTemp("", "uds-pool-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "test.socket")
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	var newConnections atomic.Int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				newConnections.Add(1)
			}
		},
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	// Ensure the server is accepting connections before starting goroutines.
	probe, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	_ = probe.Close()

	client := UDSClient(socketPath, 5*time.Second)

	const (
		numGoroutines = 50
		requestsEach  = 10
	)

	start := make(chan struct{})
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			<-start
			for range requestsEach {
				req, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
				if err != nil {
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					return
				}
				resp.Body.Close()
			}
		})
	}

	close(start)
	wg.Wait()

	// With MaxIdleConnsPerHost=100, connections are heavily reused. Ideally ~50
	// new connections (one per goroutine), but timing races between goroutines
	// competing for idle connections can push the count above that — especially
	// on Windows where scheduler and socket latency differ. The important
	// invariant is that the count is far below 500 (one per request, as would
	// happen with the old MaxIdleConnsPerHost=2 default).
	assert.LessOrEqual(t, newConnections.Load(), int64(numGoroutines*2),
		"connections should be reused; got %d new connections for %d requests",
		newConnections.Load(), numGoroutines*requestsEach)
}

// TestUDSServerCloseRecovery verifies that the UDS HTTP client recovers transparently
// from server-side connection closes, which reproduce agent backpressure / restart
// scenarios that previously caused "broken pipe" or "connection reset by peer" errors.
func TestUDSServerCloseRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping connection recovery test in short mode")
	}

	dir, err := os.MkdirTemp("", "uds-recovery-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "test.socket")
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	var requestCount atomic.Int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := requestCount.Add(1)
			// Force connection close every 5th request to simulate agent backpressure.
			if n%5 == 0 {
				w.Header().Set("Connection", "close")
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	// Ensure the server is accepting connections before starting goroutines.
	probe, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	_ = probe.Close()

	client := UDSClient(socketPath, 5*time.Second)

	const (
		numGoroutines = 20
		requestsEach  = 20
	)

	start := make(chan struct{})
	var successes atomic.Int64
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			<-start
			for range requestsEach {
				req, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
				if err != nil {
					continue
				}
				resp, err := client.Do(req)
				if err != nil {
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					successes.Add(1)
				}
			}
		})
	}

	close(start)
	wg.Wait()

	// All requests must succeed. The HTTP client transparently opens a new
	// connection after a server-forced close, so no request should be lost.
	assert.Equal(t, int64(numGoroutines*requestsEach), successes.Load(),
		"all requests should succeed despite periodic server-side connection closes")
}
