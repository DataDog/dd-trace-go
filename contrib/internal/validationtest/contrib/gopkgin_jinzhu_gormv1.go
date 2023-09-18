// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"database/sql"
	"testing"

	gormtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gopkg.in/jinzhu/gorm.v1"
	gormtest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/gorm"
	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"

	_ "github.com/lib/pq" // need pg package for sql tests
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type GoPkgGorm struct {
	numSpans int
	opts     []gormtrace.Option
}

func NewGoPkgGorm() *GoPkgGorm {
	return &GoPkgGorm{
		opts: make([]gormtrace.Option, 0),
	}
}

func (i *GoPkgGorm) Name() string {
	return "gopkg.in/jinzhu/gorm.v1"
}

func (i *GoPkgGorm) Init(t *testing.T) {
	t.Helper()
	closeFunc := sqltest.Prepare(t, gormtest.TableName)
	t.Cleanup(func() {
		closeFunc()
		i.numSpans = 0
	})
}

func (i *GoPkgGorm) GenSpans(t *testing.T) {
	operationToNumSpans := map[string]int{
		"Connect":       4,
		"Ping":          2,
		"Query":         2,
		"Statement":     7,
		"BeginRollback": 3,
		"Exec":          5,
	}
	i.numSpans += gormtest.RunAll(t, operationToNumSpans, gormtest.RegisterFunc, getDBGoPkgGorm)
}

func (i *GoPkgGorm) NumSpans() int {
	return i.numSpans
}

func getDBGoPkgGorm(t *testing.T, driverName string, connString string, _ func(*sql.DB) gorm.Dialector) *sql.DB {
	db, err := gormtrace.Open(driverName, connString)
	require.NoError(t, err)
	return db.DB()
}

func (i *GoPkgGorm) WithServiceName(name string) {
	i.opts = append(i.opts, gormtrace.WithServiceName(name))
}
