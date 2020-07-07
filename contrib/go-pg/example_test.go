// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package pg

import (
	"context"

	ddpggo "gopkg.in/DataDog/dd-trace-go.v1/contrib/pg-go/pg_go"

	"github.com/go-pg/pg/v10"
)

// To trace Cassandra commands, use our query wrapper WrapQuery.
func Example() {
	// Initialise a postgres session as usual.
	conn := pg.Connect(&pg.Options{
		User:     "postgres",
		Database: "postgres",
	})

	// Wrap connection with Hook, which catch
	Hook(conn)

	// To n is stored result, in this case 1
	var n int

	// For tracing is required execute query with context.
	_, err := conn.WithContext(context.Background()).QueryOne(pg.Scan(&n), "SELECT 1")
	if err != nil {
		panic(err)
	}
}
