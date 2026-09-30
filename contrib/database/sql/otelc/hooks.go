// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc hooks for database/sql. See otelc.yaml.
package otelc

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
	"sync/atomic"
	"time"
	_ "unsafe" // for go:linkname

	"go.opentelemetry.io/otelc/pkg/hook"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
)

//go:linkname wrapConnector github.com/DataDog/dd-trace-go/contrib/database/sql/v2.otelcWrapConnector
func wrapConnector(c driver.Connector, dsn string) (driver.Connector, bool)

//go:linkname startDBStats github.com/DataDog/dd-trace-go/contrib/database/sql/v2.otelcStartDBStats
func startDBStats(driver.Connector, *sql.DB)

//go:linkname isRegistered github.com/DataDog/dd-trace-go/contrib/database/sql/v2.otelcIsRegistered
func isRegistered(string) bool

type registration struct {
	name string
	drv  driver.Driver
}

var (
	// mu guards ready, pending, early and the fields of each earlyConnector.
	mu      sync.Mutex
	ready   bool
	pending []registration
	early   []*earlyConnector
)

func isReady() bool {
	mu.Lock()
	defer mu.Unlock()
	return ready
}

// Go initializes this package after the contrib because it imports it, so from
// here on the hooks can call the contrib directly.
func init() { setReady() }

// setReady registers the drivers that AfterRegister queued, traces the
// databases opened before init, and lets the hooks call the contrib.
func setReady() {
	mu.Lock()
	for _, r := range pending {
		sqltrace.Register(r.name, r.drv)
	}
	pending = nil
	var dbs []*sql.DB
	for _, c := range early {
		if c.trace() {
			dbs = append(dbs, c.db)
		}
	}
	early = nil
	ready = true
	mu.Unlock()

	for _, db := range dbs {
		closeIdle(db)
	}
}

func AfterRegister(ictx hook.HookContext) {
	name, _ := ictx.GetParam(0).(string)
	drv, _ := ictx.GetParam(1).(driver.Driver)
	mu.Lock()
	// Drivers call database/sql.Register from their own init(). database/sql
	// reaches this hook through //go:linkname, which is not an import, so a
	// driver like github.com/lib/pq can be initialized before the contrib.
	// sqltrace.Register would panic then, so queue the driver for init.
	if !ready {
		pending = append(pending, registration{name, drv})
		mu.Unlock()
		return
	}
	mu.Unlock()
	// otelc imports this package from main, so Go initializes it after the
	// tracer and the contrib, just before main. Drivers registered from main
	// or later reach this line.
	sqltrace.Register(name, drv)
}

type openResult struct {
	db  *sql.DB
	err error
}

// earlyDSN carries the DSN of a database/sql.Open call made before init to
// AfterOpen, which gives it to the earlyConnector of the returned DB.
type earlyDSN string

func BeforeOpen(ictx hook.HookContext, driverName, dataSourceName string) {
	if !isReady() {
		// Let database/sql.Open run. The OpenDB hook it calls sets up an
		// earlyConnector.
		ictx.SetData(earlyDSN(dataSourceName))
		return
	}
	// For a driver it does not know, sqltrace.Open calls database/sql.Open,
	// which reaches this hook again. Letting that call through stops a loop.
	if !isRegistered(driverName) {
		return
	}
	db, err := sqltrace.Open(driverName, dataSourceName)
	ictx.SetData(openResult{db, err})
	ictx.SetSkipCall(true)
}

func AfterOpen(ictx hook.HookContext, db *sql.DB, _ error) {
	switch data := ictx.GetData().(type) {
	case openResult:
		ictx.SetReturnVal(0, data.db)
		ictx.SetReturnVal(1, data.err)
	case earlyDSN:
		if db == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, c := range early {
			if c.db == db {
				c.dsn = string(data)
			}
		}
	}
}

func BeforeOpenDB(ictx hook.HookContext, c driver.Connector) {
	if !isReady() {
		// The contrib cannot be called before init, so init traces this
		// connector later.
		ec := &earlyConnector{Connector: c}
		ictx.SetParam(0, ec)
		ictx.SetData(ec)
		return
	}
	traced, wrapped := wrapConnector(c, "")
	if !wrapped {
		// The contrib's own OpenDB passes a connector it already traces.
		return
	}
	// Replace the original connector with the traced one.
	ictx.SetParam(0, traced)
	ictx.SetData(traced)
}

func AfterOpenDB(ictx hook.HookContext, db *sql.DB) {
	if db == nil {
		return
	}
	switch c := ictx.GetData().(type) {
	case *earlyConnector:
		mu.Lock()
		defer mu.Unlock()
		c.db = db
		if ready {
			// init ran while this database was being opened.
			c.trace()
			return
		}
		early = append(early, c)
	case driver.Connector:
		startDBStats(c, db)
	}
}

// earlyConnector is the connector of a database opened before init, for
// example from a package-level variable. It uses the original connector until
// init traces it, since the *sql.DB that holds it cannot be replaced.
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

// trace switches c to a traced connector, and reports whether it did. mu must
// be held.
func (c *earlyConnector) trace() bool {
	if c.closed {
		return false
	}
	traced, wrapped := wrapConnector(c.Connector, c.dsn)
	if !wrapped {
		// The contrib's own OpenDB, called before init, passed a connector it
		// already traces and collects DB stats for.
		return false
	}
	c.traced.Store(&traced)
	startDBStats(traced, c.db)
	return true
}

// closeIdle closes the idle connections of db. The original connector opened
// them before init, for example for a Ping, and database/sql would reuse them
// for later queries without tracing them.
func closeIdle(db *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for n := db.Stats().Idle; n > 0; n-- {
		conn, err := db.Conn(ctx)
		if err != nil {
			return
		}
		// database/sql closes a connection instead of putting it back in the
		// pool when Raw returns driver.ErrBadConn.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}
}
