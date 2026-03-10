// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package net

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// FreeListener returns an open TCP listener bound to a random free port on
// 127.0.0.1. The listener is left open so that the OS cannot reassign the port
// before the caller has a chance to use it. Callers should pass the listener
// directly to their server (e.g. http.Server.Serve, fasthttp.Server.Serve,
// fiber.App.Listener) rather than closing it and re-opening on the same port,
// which would reintroduce the TOCTOU race.
//
// The listener is registered for cleanup via t.Cleanup and will be closed when
// the test finishes if the server has not already claimed it.
func FreeListener(t testing.TB) net.Listener {
	t.Helper()
	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = li.Close() })
	return li
}

// FreePort returns a random free port on 127.0.0.1.
//
// Deprecated: FreePort has a TOCTOU race — it releases the port back to the OS
// before the caller can bind it, so another process may steal the port in the
// interim. Prefer FreeListener, which keeps the port reserved until the server
// is ready to claim it.
func FreePort(t testing.TB) int {
	t.Helper()
	li := FreeListener(t)
	tcpAddr, _ := li.Addr().(*net.TCPAddr)
	require.NoError(t, li.Close())
	return tcpAddr.Port
}
