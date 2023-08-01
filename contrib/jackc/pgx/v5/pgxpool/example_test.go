package pgxpool_test

import (
	"context"
	"log"

	pgxpool "gopkg.in/DataDog/dd-trace-go.v1/contrib/jackc/pgx/v5/pgxpool"
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
