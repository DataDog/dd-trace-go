// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package sql

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

type TestCase struct {
	*sql.DB
	// untraced is a connection opened with the driver directly, used to set up
	// the test case without producing unnecessary spans. It does not go through
	// database/sql, so neither orchestrion nor otelc traces it. It is retained as
	// a [TestCase] field and cleaned up using [t.Cleanup] to ensure the shared
	// cache is not lost, as it stops existing once the last connection using it
	// is closed.
	untraced driver.Conn
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	const (
		dn  = "sqlite3"
		dsn = "file::memory:?cache=shared"
	)

	var err error

	tc.untraced, err = (&sqlite3.SQLiteDriver{}).Open(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tc.untraced.Close()) })
	untraced, ok := tc.untraced.(driver.ExecerContext)
	require.True(t, ok)

	_, err = untraced.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		userid INTEGER,
		content STRING,
		created STRING
	)`, nil)
	require.NoError(t, err)

	_, err = untraced.ExecContext(ctx,
		`INSERT OR REPLACE INTO notes(userid, content, created) VALUES
		(1, 'Hello, John. This is John. You are leaving a note for yourself. You are welcome and thank you.', datetime('now')),
		(1, 'Hey, remember to mow the lawn.', datetime('now')),
		(2, 'Reminder to submit that report by Thursday.', datetime('now')),
		(2, 'Opportunities don''t happen, you create them.', datetime('now')),
		(3, 'Pick up cabbage from the store on the way home.', datetime('now')),
		(3, 'Review PR #1138', datetime('now')
	)`, nil)
	require.NoError(t, err)

	tc.DB, err = sql.Open(dn, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tc.DB.Close()) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	_, err := tc.DB.ExecContext(ctx,
		`INSERT INTO notes (userid, content, created) VALUES (?, ?, datetime('now'));`,
		1337, "This is Elite!")
	require.NoError(t, err)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"resource": "INSERT INTO notes (userid, content, created) VALUES (?, ?, datetime('now'));",
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
