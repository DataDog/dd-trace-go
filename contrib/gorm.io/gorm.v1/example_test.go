// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gorm_test

import (
	"context"
	"errors"
	"log"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
	gormtrace "github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Name string
}

func ExampleOpen() {
	// Register augments the provided driver with tracing, enabling it to be loaded by gormtrace.Open.
	sqltrace.Register("pgx", &stdlib.Driver{}, sqltrace.WithService("my-service"))
	sqlDb, err := sqltrace.Open("pgx", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	db, err := gormtrace.Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	var user User

	// All calls through gorm.DB are now traced.
	db.Where("name = ?", "jinzhu").First(&user)
}

// ExampleNewTracePlugin illustrates how to trace gorm using the gorm.Plugin api.
func ExampleNewTracePlugin() {
	// Register augments the provided driver with tracing, enabling it to be loaded by gorm.Open and the gormtrace.TracePlugin.
	sqltrace.Register("pgx", &stdlib.Driver{}, sqltrace.WithService("my-service"))
	sqlDb, err := sqltrace.Open("pgx", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	var user User

	errCheck := gormtrace.WithErrorCheck(func(err error) bool {
		return !errors.Is(err, gorm.ErrRecordNotFound)
	})
	if err := db.Use(gormtrace.NewTracePlugin(errCheck)); err != nil {
		log.Fatal(err)
	}

	// All calls through gorm.DB are now traced.
	db.Where("name = ?", "jinzhu").First(&user)
}

func ExampleContext() {
	// Register augments the provided driver with tracing, enabling it to be loaded by gormtrace.Open.
	sqltrace.Register("pgx", &stdlib.Driver{}, sqltrace.WithService("my-service"))
	sqlDb, err := sqltrace.Open("pgx", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	db, err := gormtrace.Open(postgres.New(postgres.Config{Conn: sqlDb}), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	var user User

	// Create a root span, giving name, server and resource.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "my-query",
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ServiceName("my-db"),
		tracer.ResourceName("initial-access"),
	)
	defer span.Finish()

	// Subsequent spans inherit their parent from context.
	db.WithContext(ctx).Where("name = ?", "jinzhu").First(&user)
}
