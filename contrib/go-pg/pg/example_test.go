// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package pg_test

import (
	"context"
	"log"

	pggotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-pg/pg"

	"github.com/go-pg/pg/v10"
)

// To trace Postgres query using go-pg ORM.
// Create a connection with pg.Connect and wrap instance with pggotrace.Hook
// For proper query tracing, context must be passed to query,
// where will be stored Span with information about query
func Example() {
	var user struct {
		Name string
	}
	// Create go-pg connection as usually.
	// More you can find in official documentation:
	// https://pg.uptrace.dev/
	conn := pg.Connect(&pg.Options{
		User:     "go-pg-test",
		Database: "datadog",
	})

	// Wrap pg.connect with pggotrace.Hook for start tracing postgres queries.
	pggotrace.Hook(conn)
	// For correct tracing, must be query execute with context.
	_, err := conn.WithContext(context.Background()).QueryOne(&user, "SELECT name FROM users")
	if err != nil {
		log.Fatal(err)
	}
}
