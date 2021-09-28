// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql_test

import (
	"context"
	"log"

	sqlite "github.com/mattn/go-sqlite3" // Setup application to use Sqlite

	sqltrace "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/database/sql"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

func Example() {
	// The first step is to register the driver that we will be using.
	sqltrace.Register("postgres", &pq.Driver{})

	// Followed by a call to Open.
	db, err := sqltrace.Open("postgres", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}

	// Then, we continue using the database/sql package as we normally would, with tracing.
	rows, err := db.Query("SELECT name FROM users WHERE age=?", 27)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
}

func Example_context() {
	// Register the driver that we will be using (in this case mysql) under a custom service name.
	sqltrace.Register("mysql", &mysql.MySQLDriver{}, sqltrace.WithServiceName("my-db"))

	// Open a connection to the DB using the driver we've just registered with tracing.
	db, err := sqltrace.Open("mysql", "user:password@/dbname")
	if err != nil {
		log.Fatal(err)
	}

	// Create a root span, giving name, server and resource.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "my-query",
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ServiceName("my-db"),
		tracer.ResourceName("initial-access"),
	)

	// Subsequent spans inherit their parent from context.
	rows, err := db.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	if err != nil {
		log.Fatal(err)
	}
	rows.Close()
	span.Finish(tracer.WithError(err))
}

func Example_sqlite() {
	// Register the driver that we will be using (in this case Sqlite) under a custom service name.
	sqltrace.Register("sqlite", &sqlite.SQLiteDriver{}, sqltrace.WithServiceName("sqlite-example"))

	// Open a connection to the DB using the driver we've just registered with tracing.
	db, err := sqltrace.Open("sqlite", "./test.db")
	if err != nil {
		log.Fatal(err)
	}

	// Create a root span, giving name, server and resource.
	span, ctx := tracer.StartSpanFromContext(context.Background(), "my-query",
		tracer.SpanType("example"),
		tracer.ServiceName("sqlite-example"),
		tracer.ResourceName("initial-access"),
	)

	// Subsequent spans inherit their parent from context.
	rows, err := db.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	if err != nil {
		log.Fatal(err)
	}
	rows.Close()
	span.Finish(tracer.WithError(err))
}
