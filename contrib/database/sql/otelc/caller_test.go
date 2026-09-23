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

// probeDriver records calledByContrib each time OpenConnector runs.
// database/sql.Open calls it synchronously, so it stands in for a hook.
type probeDriver struct {
	byContrib *[]bool
}

func (probeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (d probeDriver) OpenConnector(string) (driver.Connector, error) {
	*d.byContrib = append(*d.byContrib, calledByContrib())
	return probeConnector{d}, nil
}

type probeConnector struct {
	d probeDriver
}

func (probeConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (c probeConnector) Driver() driver.Driver { return c.d }

func TestCalledByContrib(t *testing.T) {
	var byContrib []bool
	sql.Register("otelc-probe", probeDriver{&byContrib})

	db, err := sql.Open("otelc-probe", "")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	assert.Equal(t, []bool{false}, byContrib, "application call")

	// The driver is unknown to the contrib, since plain go test weaves no
	// Register hook, so the contrib discovers it with database/sql.Open and
	// then calls OpenConnector itself.
	byContrib = nil
	db, err = sqltrace.Open("otelc-probe", "")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	assert.Equal(t, []bool{true, false}, byContrib, "contrib call")
}
