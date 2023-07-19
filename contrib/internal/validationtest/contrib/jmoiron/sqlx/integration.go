// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package gorm

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	gormtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/gorm"
	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"
	"gorm.io/gorm"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	sqlxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/jmoiron/sqlx"

	_ "github.com/lib/pq" // need pg package for sql tests
	"github.com/stretchr/testify/require"
)

type Integration struct {
	numSpans int
	opts     []sqltrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]sqltrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "jmoiron/sqlx"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	opts = i.opts

	closeFunc := sqltest.Prepare(t, gormtest.TableName)
	t.Cleanup(func() {
		closeFunc()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	operationToNumSpans := map[string]int{
		"Connect":       2,
		"Ping":          2,
		"Query":         2,
		"Statement":     7,
		"BeginRollback": 3,
		"Exec":          5,
	}
	i.numSpans += gormtest.RunAll(t, operationToNumSpans, registerFunc, getDB)
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, sqltrace.WithServiceName(name))
}

var opts []sqltrace.Option

func registerFunc(driverName string, driver driver.Driver) {
	sqltrace.Register(driverName, driver, opts...)
}

func getDB(t *testing.T, driverName string, connString string, _ func(*sql.DB) gorm.Dialector) *sql.DB {
	db, err := sqlxtrace.Open(driverName, connString, opts...)
	require.NoError(t, err)
	return db.DB
}
