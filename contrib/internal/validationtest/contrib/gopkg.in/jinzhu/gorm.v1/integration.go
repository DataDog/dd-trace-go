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

	_ "github.com/lib/pq"
	gormtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/gorm"
	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"
	"gorm.io/gorm"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	gormtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gopkg.in/jinzhu/gorm.v1"
)

type Integration struct {
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/gopkg.in/jinzhu/gorm.v1"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	close_func := sqltest.Prepare(gormtest.TableName)
	return func() {
		close_func()
	}
}

func (i *Integration) GenSpans(t *testing.T) {
	i.numSpans = gormtest.RunAll(i.numSpans, t, registerFunc, getDB)
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func registerFunc(driverName string, driver driver.Driver) {
	sqltrace.Register(driverName, driver)
}

func getDB(driverName string, connString string, _ func(*sql.DB) gorm.Dialector) *sql.DB {
	db, err := gormtrace.Open(driverName, connString)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	return db.DB()
}
