// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package gormv2_test

import (
	"database/sql"
	"github.com/jackc/pgx/v4/stdlib"
	"gorm.io/driver/postgres"
	"log"

	"gorm.io/gorm"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	gormtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorm.io"
)

func postgresDialector(db *sql.DB) gorm.Dialector {
	return postgres.New(postgres.Config{Conn: db})
}

func ExampleOpen() {
	// Register augments the provided driver with tracing, enabling it to be loaded by gormtrace.Open.
	sqltrace.Register("postgres", &stdlib.Driver{}, sqltrace.WithServiceName("my-service"))

	// Open the registered driver, allowing all uses of the returned *gorm.DB to be traced.
	db, err := gormtrace.Open(postgresDialector, "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}

	user := struct {
		gorm.Model
		Name string
	}{}

	// All calls through gorm.DB are now traced.
	db.Where("name = ?", "jinzhu").First(&user)
}
