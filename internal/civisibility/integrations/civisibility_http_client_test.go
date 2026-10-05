// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"

	"github.com/stretchr/testify/require"
)

func TestExitCiVisibilityClosesCIVisibilityHTTPClientIdleConnections(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)

	idleConn := make(chan struct{}, 1)
	closedConn := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(civisibilitynet.HeaderContentType, civisibilitynet.ContentTypeJSON)
		_, _ = w.Write([]byte(`{}`))
	}))
	server.Config.ConnState = func(_ stdnet.Conn, state http.ConnState) {
		switch state {
		case http.StateIdle:
			signalCIVisibilityHTTPConnectionState(idleConn)
		case http.StateClosed:
			signalCIVisibilityHTTPConnectionState(closedConn)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	response, err := civisibilitynet.NewRequestHandler().SendRequest(civisibilitynet.RequestConfig{
		Method: "GET",
		URL:    server.URL,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	waitForCIVisibilityHTTPConnectionState(t, idleConn, "idle")

	civisibility.SetState(civisibility.StateInitialized)
	ExitCiVisibility()

	require.Equal(t, civisibility.StateExited, civisibility.GetState())
	waitForCIVisibilityHTTPConnectionState(t, closedConn, "closed")
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
// connection-state transition produced during CI Visibility shutdown.
func waitForCIVisibilityHTTPConnectionState(t *testing.T, state <-chan struct{}, stateName string) {
	t.Helper()
	select {
	case <-state:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for connection to become %s", stateName)
	}
}
