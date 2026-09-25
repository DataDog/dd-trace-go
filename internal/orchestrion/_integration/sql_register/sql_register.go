// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sql_register

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCase checks that a driver registering itself from its own init() is
// known to the contrib before the application opens a database. OpenDB only
// gets a connector, so the contrib names the driver from its registry, and
// falls back to the driver's Go type when the driver is not registered.
//
// It lives in its own package because any database/sql.Open in the same test
// binary also registers the driver, which would hide a failure here.
type TestCase struct {
	*sql.DB
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.DB = sql.OpenDB(connector{dsn: "file::memory:"})
	t.Cleanup(func() { assert.NoError(t, tc.DB.Close()) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	_, err := tc.DB.ExecContext(ctx, "SELECT 1")
	require.NoError(t, err)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"resource": "SELECT 1",
				"type":     "sql",
				"name":     "sqlite3.query",
				"service":  "sqlite3.db",
			},
			Meta: map[string]string{
				"component":      "database/sql",
				"span.kind":      "client",
				"sql.query_type": "Exec",
			},
		},
	}
}

type connector struct {
	dsn string
}

func (c connector) Connect(context.Context) (driver.Conn, error) {
	return c.Driver().Open(c.dsn)
}

func (connector) Driver() driver.Driver {
	return &sqlite3.SQLiteDriver{}
}
