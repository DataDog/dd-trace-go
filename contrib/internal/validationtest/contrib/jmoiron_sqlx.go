// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	gormtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/gorm"
	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"
	sqlxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/jmoiron/sqlx"

	_ "github.com/lib/pq" // need pg package for sql tests
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type SQLX struct {
	numSpans int
	opts     []sqltrace.Option
}

func NewSQLX() *SQLX {
	return &SQLX{
		opts: make([]sqltrace.Option, 0),
	}
}

func (i *SQLX) Name() string {
	return "jmoiron/sqlx"
}

func (i *SQLX) Init(t *testing.T) {
	t.Helper()
	sqlxOpts = i.opts

	closeFunc := sqltest.Prepare(t, gormtest.TableName)
	t.Cleanup(func() {
		closeFunc()
		i.numSpans = 0
	})
}

func (i *SQLX) GenSpans(t *testing.T) {
	operationToNumSpans := map[string]int{
		"Connect":       2,
		"Ping":          2,
		"Query":         2,
		"Statement":     7,
		"BeginRollback": 3,
		"Exec":          5,
	}
	i.numSpans += gormtest.RunAll(t, operationToNumSpans, registerFuncSQLX, getDBSQLX)
}

func (i *SQLX) NumSpans() int {
	return i.numSpans
}

func (i *SQLX) WithServiceName(name string) {
	i.opts = append(i.opts, sqltrace.WithServiceName(name))
}

var sqlxOpts []sqltrace.Option

func registerFuncSQLX(driverName string, driver driver.Driver) {
	sqltrace.Register(driverName, driver, sqlxOpts...)
}

func getDBSQLX(t *testing.T, driverName string, connString string, _ func(*sql.DB) gorm.Dialector) *sql.DB {
	db, err := sqlxtrace.Open(driverName, connString, sqlxOpts...)
	require.NoError(t, err)
	return db.DB
}
