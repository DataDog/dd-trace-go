// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type otelcTestDriver struct{}

func (otelcTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

type otelcTestConnector struct{}

func (otelcTestConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (otelcTestConnector) Driver() driver.Driver { return otelcTestDriver{} }

func TestOtelcWrapConnector(t *testing.T) {
	traced, wrapped := otelcWrapConnector(otelcTestConnector{})
	require.True(t, wrapped)
	assert.IsType(t, &tracedConnector{}, traced)

	again, wrapped := otelcWrapConnector(traced)
	assert.False(t, wrapped, "a traced connector must not be wrapped twice")
	assert.Same(t, traced, again)
}

func TestOtelcStartDBStats(t *testing.T) {
	traced, _ := otelcWrapConnector(otelcTestConnector{})
	db := sql.OpenDB(traced)
	// DB stats are off by default, so this starts nothing, and a connector
	// that is not traced is ignored.
	otelcStartDBStats(traced, db)
	otelcStartDBStats(otelcTestConnector{}, db)
	require.NoError(t, db.Close())
}

func TestOtelcIsRegistered(t *testing.T) {
	const name = "otelc-test-driver"
	t.Cleanup(func() { unregister(name) })

	assert.False(t, otelcIsRegistered(name))
	Register(name, otelcTestDriver{})
	assert.True(t, otelcIsRegistered(name))
}
