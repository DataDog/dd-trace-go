// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tracer

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// connCountListener counts the open TCP connections of a test server. Every
// accepted connection increments the counter; every close decrements it.
type connCountListener struct {
	net.Listener
	open atomic.Int64
}

func (l *connCountListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.open.Add(1)
	return &connCountConn{Conn: c, listener: l}, nil
}

type connCountConn struct {
	net.Conn
	listener *connCountListener
	once     sync.Once
}

func (c *connCountConn) Close() error {
	c.once.Do(func() { c.listener.open.Add(-1) })
	return c.Conn.Close()
}

// TestTracerStopQuiescesAppStartedTelemetryFlush verifies that tracer.Stop
// waits for the app-started telemetry flush to complete.
//
// telemetry.StartApp sends the app-started message from a background
// goroutine. When a tracer starts and stops while that flush runs, Stop
// reaches its final (*http.Client).CloseIdleConnections call before the
// flush connection returns to the idle pool. The close skips the busy
// connection, and nothing closes it after the flush completes. The orphaned
// keep-alive connection then lives until the transport IdleConnTimeout
// elapses, and goleak reports it as a leak.
//
// The mock agent delays telemetry requests so the app-started flush is busy
// when Stop runs. The test then waits for the flush and asserts that no
// connection to the mock agent stays open. A Stop that does not join the
// app-started flush fails this assertion because the flushed connection
// parks in the idle pool with no one left to close it.
func TestTracerStopQuiescesAppStartedTelemetryFlush(t *testing.T) {
	var telemetryHandled atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/apmtelemetry") {
			// Keep the app-started flush busy past tracer.Stop().
			defer telemetryHandled.Store(true)
			time.Sleep(300 * time.Millisecond)
		}
		// Drain the request body so the connection stays reusable.
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"7.99.0","endpoints":["/v1.0/traces","/telemetry/proxy/"],"client_drop_p0s":true}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	counting := &connCountListener{Listener: listener}
	srv.Listener = counting
	srv.Start()
	defer srv.Close()

	agentURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	// Earlier tests in this package leave the global telemetry client set,
	// because tracer.Stop closes the client but does not clear the global.
	// StartApp ignores a new client while the global one is set, so reset the
	// global client first. This makes the app-started flush of this tracer
	// deterministic.
	t.Cleanup(func() { telemetry.SwapClient(nil) })
	telemetry.SwapClient(nil)

	Start(WithAgentAddr(agentURL.Host), withNoopStats())
	Stop()

	// Wait until the delayed telemetry request completed on the server side.
	require.Eventually(t, func() bool { return telemetryHandled.Load() },
		5*time.Second, 10*time.Millisecond, "no telemetry request reached the mock agent")
	// Give the client time to park the flushed connection in the idle pool,
	// and a Stop that joined the flush time to close it.
	time.Sleep(500 * time.Millisecond)

	assert.Zero(t, counting.open.Load(),
		"tracer.Stop left a keep-alive connection open: the asynchronous app-started telemetry flush was still busy when Stop closed idle connections")
}
