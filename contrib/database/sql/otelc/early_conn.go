// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync/atomic"
)

// earlyDSN carries the DSN of a database/sql.Open call made before init to
// AfterOpen, which gives it to the earlyConnector of the returned DB.
type earlyDSN string

// earlyConnector is the connector of a database opened before init, for
// example from a package-level variable. It uses the original connector until
// init traces it, since the *sql.DB that holds it cannot be replaced.
//
// Known limitation: connections opened before init, for example by a Ping in a
// package init, stay untraced until database/sql retires them. Closing them
// would lose per-connection state the app may rely on, such as an in-memory
// SQLite schema, temporary tables or session settings.
type earlyConnector struct {
	driver.Connector

	// traced is set once, by init.
	traced atomic.Pointer[driver.Connector]
	dsn    string
	db     *sql.DB
	closed bool
}

func (c *earlyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if t := c.traced.Load(); t != nil {
		return (*t).Connect(ctx)
	}
	return c.Connector.Connect(ctx)
}

// Close is called by (*sql.DB).Close.
func (c *earlyConnector) Close() error {
	mu.Lock()
	c.closed = true
	var target any = c.Connector
	if t := c.traced.Load(); t != nil {
		target = *t
	}
	// The connector's Close is application code, which can close another
	// early database and take mu again.
	mu.Unlock()
	if cl, ok := target.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// trace switches c to a traced connector. mu must be held.
func (c *earlyConnector) trace() {
	if c.closed {
		return
	}
	traced, wrapped := wrapConnector(c.Connector, c.dsn)
	if !wrapped {
		// The contrib's own OpenDB, called before init, passed a connector it
		// already traces and collects DB stats for.
		return
	}
	c.traced.Store(&traced)
	startDBStats(traced, c.db)
}
