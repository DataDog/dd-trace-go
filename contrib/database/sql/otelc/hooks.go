// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc hooks for database/sql. See otelc.yaml.
package otelc

import (
	"database/sql"
	"database/sql/driver"
	"sync"
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
	// mu guards ready, pending, early, earlyOpens and the fields of each
	// earlyConnector. setReady holds it while it calls into the contrib, so no
	// code reachable from sqltrace.Register, wrapConnector or startDBStats may
	// call database/sql.Register, Open or OpenDB: their hooks take mu too.
	mu      sync.Mutex
	ready   bool
	pending []registration
	early   []*earlyConnector
	// earlyOpens counts database/sql.Open calls that started before init and
	// have not returned. Their connectors wait in early until AfterOpen has
	// given them the DSN, even if init runs in the meantime.
	earlyOpens int
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
	if earlyOpens == 0 {
		traceEarly()
	}
	ready = true
	mu.Unlock()
}

// traceEarly traces the connectors in early. mu must be held.
func traceEarly() {
	for _, c := range early {
		c.trace()
	}
	early = nil
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

func BeforeOpen(ictx hook.HookContext, driverName, dataSourceName string) {
	mu.Lock()
	if !ready {
		// Let database/sql.Open run. The OpenDB hook it calls sets up an
		// earlyConnector.
		earlyOpens++
		mu.Unlock()
		ictx.SetData(earlyDSN(dataSourceName))
		return
	}
	mu.Unlock()
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
		mu.Lock()
		earlyOpens--
		if db != nil {
			for _, c := range early {
				if c.db == db {
					c.dsn = string(data)
				}
			}
		}
		// init ran while database/sql.Open was running, and skipped the
		// connectors waiting for a DSN.
		if ready && earlyOpens == 0 {
			traceEarly()
		}
		mu.Unlock()
	}
}

func BeforeOpenDB(ictx hook.HookContext, c driver.Connector) {
	mu.Lock()
	waiting := !ready || earlyOpens > 0
	mu.Unlock()
	if waiting {
		// The contrib cannot be called before init, so init traces this
		// connector later. An Open that started before init also waits, so
		// that it keeps its DSN.
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
		if ready && earlyOpens == 0 {
			// init ran while this database was being opened.
			c.trace()
			return
		}
		early = append(early, c)
	case driver.Connector:
		startDBStats(c, db)
	}
}
