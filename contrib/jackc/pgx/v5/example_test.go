package pgx_test

import (
	"context"
	"log"

	pgxtracer "gopkg.in/DataDog/dd-trace-go.v1/contrib/jackc/pgx/v5"

	"github.com/jackc/pgx/v5"
)

func Example() {
	ctx := context.TODO()

	// The package exposes the same connect functions as the regular pgx library
	// which sets up a tracer and connects as usual.
	db, err := pgxtracer.Connect(ctx, "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close(ctx)

	// Any calls made to the database will be traced as expected.
	rows, err := db.Query(ctx, "SELECT name FROM users WHERE age=?", 27)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	// This enables you to use PostgresSQL specific functions implemented by pgx.
	numbers := []int{1, 2, 3}

	copyFromSource := pgx.CopyFromSlice(len(numbers), func(i int) ([]any, error) {
		return []any{numbers[i]}, nil
	})

	_, err = db.CopyFrom(ctx, []string{"numbers"}, []string{"number"}, copyFromSource)
	if err != nil {
		log.Fatal(err)
	}
}
