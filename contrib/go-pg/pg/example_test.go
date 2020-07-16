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

// To trace Postgres queries, wrap pg.Connect instance with pggotrace.Hook.
func Example() {
	var user struct {
		Name string
	}
	conn := pg.Connect(&pg.Options{
		User:     "pggotest",
		Database: "datadog",
	})

	// Wrap connection with Hook, which catch
	pggotrace.Hook(conn)
	_, err := conn.WithContext(context.Background()).QueryOne(&user, "SELECT name FROM users")
	if err != nil {
		log.Fatal(err)
	}
}
