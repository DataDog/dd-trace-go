// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package gorm

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	_ "github.com/lib/pq" // need pg package for sql tests
	"github.com/stretchr/testify/require"
	gormtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/gorm"
	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"
	"gorm.io/gorm"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	gormtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorm.io/gorm.v1"
)

type Integration struct {
	numSpans int
	opts     []gormtrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]gormtrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "gopkg.in/jinzhu/gorm.v1"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	closeFunc := sqltest.Prepare(t, gormtest.TableName)
	t.Cleanup(func() {
		closeFunc()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	operationToNumSpans := map[string]int{
		"Connect":       5,
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

func registerFunc(driverName string, driver driver.Driver) {
	sqltrace.Register(driverName, driver)
}

func getDB(t *testing.T, driverName string, connString string, dialectorFunc func(*sql.DB) gorm.Dialector) *sql.DB {
	sqlDb, err := sqltrace.Open(driverName, connString)
	require.NoError(t, err)
	db, err := gormtrace.Open(dialectorFunc(sqlDb), &gorm.Config{})
	require.NoError(t, err)

	internalDB, err := db.DB()
	require.NoError(t, err)
	return internalDB
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, gormtrace.WithServiceName(name))
}
