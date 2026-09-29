// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/sql/registerdb"
)

// TestCaseRegister checks that a driver registering itself from its own init()
// is known to the contrib before the application opens a database. OpenDB only
// gets a connector, so the contrib names the driver from its registry by Go
// type, and falls back to the type name when the driver is not registered.
// Nothing else opens registerdb's driver by name, so only its sql.Register call
// can register it.
type TestCaseRegister struct {
	*sql.DB
}

func (tc *TestCaseRegister) Setup(_ context.Context, t *testing.T) {
	tc.DB = sql.OpenDB(registerConnector{dsn: "file::memory:"})
	t.Cleanup(func() { assert.NoError(t, tc.DB.Close()) })
}

func (tc *TestCaseRegister) Run(ctx context.Context, t *testing.T) {
	_, err := tc.DB.ExecContext(ctx, "SELECT 1")
	require.NoError(t, err)
}

func (*TestCaseRegister) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"resource": "SELECT 1",
				"type":     "sql",
				"name":     registerdb.Name + ".query",
				"service":  registerdb.Name + ".db",
			},
			Meta: map[string]string{
				"component":      "database/sql",
				"span.kind":      "client",
				"sql.query_type": "Exec",
			},
		},
	}
}

type registerConnector struct {
	dsn string
}

func (c registerConnector) Connect(context.Context) (driver.Conn, error) {
	return c.Driver().Open(c.dsn)
}

func (registerConnector) Driver() driver.Driver {
	return &registerdb.Driver{}
}
