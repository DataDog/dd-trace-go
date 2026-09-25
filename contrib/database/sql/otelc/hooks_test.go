// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
)

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

type testConnector struct{}

func (testConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (testConnector) Driver() driver.Driver { return testDriver{} }

// TestContribLinks fails to link if a go:linkname declaration in hooks.go no
// longer matches the contrib function it points at.
func TestContribLinks(t *testing.T) {
	traced, wrapped := wrapConnector(testConnector{})
	require.True(t, wrapped)
	_, wrapped = wrapConnector(traced)
	assert.False(t, wrapped, "a traced connector must not be wrapped twice")

	db := sql.OpenDB(traced)
	startDBStats(traced, db)
	require.NoError(t, db.Close())

	const name = "otelc-hooks-test-driver"
	assert.False(t, isRegistered(name))
	sqltrace.Register(name, testDriver{})
	assert.True(t, isRegistered(name))
}
