// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package sqlx

import (
	"fmt"
	"log"
	"os"
	"testing"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const tableName = "testsqlx"

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	defer sqltest.Prepare(tableName)()
	os.Exit(m.Run())
}

func TestMySQL(t *testing.T) {
	sqltrace.Register("mysql", &mysql.MySQLDriver{}, sqltrace.WithServiceName("mysql-test"))
	dbx, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	if err != nil {
		log.Fatal(err)
	}
	defer dbx.Close()

	testConfig := &sqltest.Config{
		DB:         dbx.DB,
		DriverName: "mysql",
		TableName:  tableName,
		ExpectName: "mysql.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "mysql-test",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "3306",
			ext.DBUser:      "test",
			ext.DBName:      "test",
		},
	}
	sqltest.RunAll(t, testConfig)
}

func TestPostgres(t *testing.T) {
	sqltrace.Register("postgres", &pq.Driver{})
	dbx, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer dbx.Close()

	testConfig := &sqltest.Config{
		DB:         dbx.DB,
		DriverName: "postgres",
		TableName:  tableName,
		ExpectName: "postgres.query",
		ExpectTags: map[string]interface{}{
			ext.ServiceName: "postgres.db",
			ext.SpanType:    ext.SpanTypeSQL,
			ext.TargetHost:  "127.0.0.1",
			ext.TargetPort:  "5432",
			ext.DBUser:      "postgres",
			ext.DBName:      "postgres",
		},
	}
	sqltest.RunAll(t, testConfig)
}
