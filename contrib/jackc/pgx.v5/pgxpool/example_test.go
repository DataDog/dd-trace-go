// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgxpool_test

import (
	"context"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/jackc/pgx.v5/pgxpool"
	"log"
)

func Example() {
	ctx := context.TODO()

	// The pgxpool uses the same tracer and is exposed the same way.
	pool, err := pgxpool.New(ctx, "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	var x int

	err = pool.QueryRow(ctx, "SELECT 1").Scan(&x)
	if err != nil {
		log.Fatal(err)
	}
}
