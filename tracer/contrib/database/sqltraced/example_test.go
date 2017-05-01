package sqltraced

import (
	"context"
	"database/sql"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/lib/pq"
)

func ExampleDB() {
	Register("Postgres", "test", &pq.Driver{}, nil)
	db, _ := sql.Open("Postgres", test.PostgresConfig.DSN())
	defer db.Close()

	span := tracer.NewRootSpan("postgres.parent", "test", "query-parent")
	ctx := tracer.ContextWithSpan(context.Background(), span)

	db.PingContext(ctx)
	rows, _ := db.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	rows.Close()

	span.Finish()
}

func ExampleTracedStmt() {
	Register("Postgres", "test", &pq.Driver{}, nil)
	db, _ := sql.Open("Postgres", test.PostgresConfig.DSN())
	defer db.Close()

	span := tracer.NewRootSpan("postgres.parent", "test", "statement-parent")
	ctx := tracer.ContextWithSpan(context.Background(), span)

	stmt, _ := db.PrepareContext(ctx, "INSERT INTO city(name) VALUES($1)")
	stmt.Exec("New York")
	stmt, _ = db.PrepareContext(ctx, "SELECT name FROM city LIMIT $1")
	rows, _ := stmt.Query(1)
	rows.Close()
	stmt.Close()

	span.Finish()
}

func ExampleTracedTx() {
	Register("Postgres", "test", &pq.Driver{}, nil)
	db, _ := sql.Open("Postgres", test.PostgresConfig.DSN())
	defer db.Close()

	span := tracer.NewRootSpan("postgres.parent", "test", "transaction-parent")
	ctx := tracer.ContextWithSpan(context.Background(), span)

	tx, _ := db.BeginTx(ctx, nil)
	tx.Rollback()

	tx, _ = db.BeginTx(ctx, nil)
	tx.ExecContext(ctx, "INSERT INTO city(name) VALUES('New York')")
	rows, _ := tx.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	rows.Close()
	stmt, _ := tx.PrepareContext(ctx, "SELECT name FROM city LIMIT $1")
	rows, _ = stmt.Query(1)
	rows.Close()
	stmt.Close()
	tx.Commit()

	span.Finish()
}
