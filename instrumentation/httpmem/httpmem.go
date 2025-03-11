// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package httpmem provides an in-memory HTTP server and client, for testing
package httpmem

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

// ServerAndClient creates an in-memory HTTP server, and a client which will
// connect to it. The server is ready to accept requests. The server should be
// closed to clean up associated resources.
func ServerAndClient(h http.Handler) (*http.Server, *http.Client) {
	p := newConnPool()
	c := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return p.NewConn()
			},
		},
	}
	s := &http.Server{
		Handler: h,
	}
	// NB: no further synchronization is required to make sure s is ready to
	// accept connections. There is already synchronization through the
	// connPool's connection channel, so NewConn and Accept won't return until
	// there is a matching call to the other, or until p is closed.
	go s.Serve(p)
	return s, c
}

// connPool creates bi-directional in-memory connections, for use by a matched
// HTTP client and server.
type connPool struct {
	p      chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newConnPool() *connPool {
	return &connPool{
		p:      make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

var errClosed = errors.New("server no longer accepting connections")

// NewConn returns a client connection, which will be matched with a
// connection created by a call to Accept.
func (c *connPool) NewConn() (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case c.p <- server:
		return client, nil
	case <-c.closed:
		return nil, errClosed
	}
}

// Accept returns a server connection, which will be matched with
// a connection created by a call to NewConn.
func (c *connPool) Accept() (net.Conn, error) {
	select {
	case server := <-c.p:
		return server, nil
	case <-c.closed:
		return nil, errClosed
	}
}

// Close causes the connection pool to stop accepting connections.
func (c *connPool) Close() error {
	c.once.Do(func() {
		close(c.closed)
	})
	return nil
}

type inMemoryAddr struct{}

func (inMemoryAddr) Network() string { return "in-memory" }
func (inMemoryAddr) String() string  { return "in-memory" }

// Addr returns a placeholder address to satisfy the net.Listener interface.
func (c *connPool) Addr() net.Addr {
	return &inMemoryAddr{}
}
