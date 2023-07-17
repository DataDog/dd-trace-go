// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gorm

import (
	"database/sql"
	"database/sql/driver"
	"log"
	"testing"

	gormtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/gorm"
	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"
	"gorm.io/gorm"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	sqlxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/jmoiron/sqlx"

	_ "github.com/lib/pq"
)

type Integration struct {
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/jmoiron/sqlx"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	closeFunc := sqltest.Prepare(gormtest.TableName)
	return func() {
		closeFunc()
	}
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
	i.numSpans += gormtest.RunAll(operationToNumSpans, t, registerFunc, getDB)
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func registerFunc(driverName string, driver driver.Driver) {
	sqltrace.Register(driverName, driver)
}

func getDB(driverName string, connString string, _ func(*sql.DB) gorm.Dialector) *sql.DB {
	db, err := sqlxtrace.Open(driverName, connString)
	if err != nil {
		log.Fatal(err)
	}
	return db.DB
}
