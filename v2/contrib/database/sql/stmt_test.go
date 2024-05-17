// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestUseStatementContext(t *testing.T) {
	Register("sqlite3", &sqlite3.SQLiteDriver{})
	db, err := Open("sqlite3", "file::memory:?cache=shared")
	require.NoError(t, err)
	defer db.Close()

	// Prepare using ctx1
	ctx1, cancel := context.WithCancel(context.Background())
	stmt, err := db.PrepareContext(ctx1, "SELECT 1")
	require.NoError(t, err)
	defer stmt.Close()
	cancel()

	// Query stmt using ctx2
	ctx2 := context.Background()
	var result int
	require.NoError(t, stmt.QueryRowContext(ctx2).Scan(&result))
}
