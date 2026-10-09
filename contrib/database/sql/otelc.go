// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sql

import (
	"database/sql"
	"database/sql/driver"
)

// The functions below are used by go:linkname in the otelc hooks
// (./otelc/hooks.go), which hook database/sql itself and cannot call OpenDB
// without calling back into their own hook.

// otelcWrapConnector returns c wrapped for tracing and true, or c and false
// when c is already a traced connector, as it is when OpenDB calls
// database/sql.OpenDB. A non-empty dsn is used as with WithDSN.
func otelcWrapConnector(c driver.Connector, dsn string) (driver.Connector, bool) {
	if _, ok := c.(*tracedConnector); ok {
		return c, false
	}
	if dsn == "" {
		return newTracedConnector(c), true
	}
	return newTracedConnector(c, WithDSN(dsn)), true
}

// otelcStartDBStats does what OpenDB does once database/sql.OpenDB returns.
func otelcStartDBStats(c driver.Connector, db *sql.DB) {
	if tc, ok := c.(*tracedConnector); ok {
		tc.startDBStats(db)
	}
}

// otelcIsRegistered reports whether Register already knows driverName, in
// which case Open does not call database/sql.Open.
func otelcIsRegistered(driverName string) bool {
	return registeredDrivers.isRegistered(driverName)
}
