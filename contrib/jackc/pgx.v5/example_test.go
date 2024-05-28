// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx_test

import (
	"context"
	"log"

	pgxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/jackc/pgx.v5"

	"github.com/jackc/pgx/v5"
)

func ExampleConnect() {
	ctx := context.TODO()

	// The package exposes the same connect functions as the regular pgx.v5 library
	// which sets up a tracer and connects as usual.
	db, err := pgxtrace.Connect(ctx, "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close(ctx)

	// Any calls made to the database will be traced as expected.
	rows, err := db.Query(ctx, "SELECT name FROM users WHERE age=$1", 27)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	// This enables you to use PostgresSQL specific functions implemented by pgx.v5.
	numbers := []int{1, 2, 3}

	copyFromSource := pgx.CopyFromSlice(len(numbers), func(i int) ([]any, error) {
		return []any{numbers[i]}, nil
	})

	_, err = db.CopyFrom(ctx, []string{"numbers"}, []string{"number"}, copyFromSource)
	if err != nil {
		log.Fatal(err)
	}
}

func ExamplePool() {
	ctx := context.TODO()

	// The pgxpool uses the same tracer and is exposed the same way.
	pool, err := pgxtrace.NewPool(ctx, "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
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
