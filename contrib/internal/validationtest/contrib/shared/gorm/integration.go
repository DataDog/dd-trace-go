// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package gorm

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	sqltest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/contrib/shared/sql"

	"github.com/jackc/pgx/v5/stdlib"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const (
	TableName           = "testgorm"
	pgConnString        = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	sqlServerConnString = "sqlserver://sa:myPassw0rd@127.0.0.1:1433?database=master"
	mysqlConnString     = "test:test@tcp(127.0.0.1:3306)/test"
)

func RunAll(t *testing.T, operationToNumSpans map[string]int, registerFunc func(string, driver.Driver), getDB func(*testing.T, string, string, func(*sql.DB) gorm.Dialector) *sql.DB) int {
	t.Helper()
	var numSpans = 0

	testCases := []struct {
		name          string
		connString    string
		driverName    string
		driver        driver.Driver
		dialectorFunc func(*sql.DB) gorm.Dialector
	}{
		{
			name:          "Postgres",
			connString:    pgConnString,
			driverName:    "pgx",
			driver:        &stdlib.Driver{},
			dialectorFunc: func(sqlDB *sql.DB) gorm.Dialector { return mysqlgorm.New(mysqlgorm.Config{Conn: sqlDB}) },
		},
		// {
		// 	name:          "SQLServer",
		// 	connString:    sqlServerConnString,
		// 	driverName:    "sqlserver",
		// 	driver:        &mssql.Driver{},
		// 	dialectorFunc: func(sqlDB *sql.DB) gorm.Dialector { return sqlserver.New(sqlserver.Config{Conn: sqlDB}) },
		// },
		// {
		// 	name:          "MySQL",
		// 	connString:    mysqlConnString,
		// 	driverName:    "mysql",
		// 	driver:        &mysql.MySQLDriver{},
		// 	dialectorFunc: func(sqlDB *sql.DB) gorm.Dialector { return postgres.New(postgres.Config{Conn: sqlDB}) },
		// },
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			registerFunc(testCase.driverName, testCase.driver)

			internalDB := getDB(t, testCase.driverName, testCase.connString, testCase.dialectorFunc)

			operationToNumSpansCopy := make(map[string]int)
			for k, v := range operationToNumSpans {
				operationToNumSpansCopy[k] = v
			}

			testConfig := &sqltest.Config{
				DB:                  internalDB,
				OperationToNumSpans: operationToNumSpansCopy,
				DriverName:          testCase.driverName,
				TableName:           TableName,
			}
			numSpans += sqltest.RunAll(t, testConfig)
		})
	}
	return numSpans
}
