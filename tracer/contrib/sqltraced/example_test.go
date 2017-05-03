package sqltraced

import (
	"context"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/lib/pq"
)

func Example() {
	// You first have to register a traced version of the driver.
	// Make sure the `name` you register it is different from the
	// original driver name. E.g. "Postgres" != "postgres"
	Register("Postgres", &pq.Driver{}, nil)

	// When calling sql.Open(), you need to specify the name of the traced driver.
	db, _ := Open("Postgres", test.PostgresConfig.DSN(), "test")
	defer db.Close()

	// We create here a parent span for the purpose of the example
	span := tracer.NewRootSpan("postgres.parent", "test", "query-parent")
	ctx := tracer.ContextWithSpan(context.Background(), span)

	// We need to use the `context` version of the database/sql API
	// in order to link this call with the parent span.
	db.PingContext(ctx)
	rows, _ := db.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	rows.Close()

	stmt, _ := db.PrepareContext(ctx, "INSERT INTO city(name) VALUES($1)")
	stmt.Exec("New York")
	stmt, _ = db.PrepareContext(ctx, "SELECT name FROM city LIMIT $1")
	rows, _ = stmt.Query(1)
	rows.Close()
	stmt.Close()

	tx, _ := db.BeginTx(ctx, nil)
	tx.ExecContext(ctx, "INSERT INTO city(name) VALUES('New York')")
	rows, _ = tx.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	rows.Close()
	stmt, _ = tx.PrepareContext(ctx, "SELECT name FROM city LIMIT $1")
	rows, _ = stmt.Query(1)
	rows.Close()
	stmt.Close()
	tx.Commit()

	// Calling span.Finish() will send the span into the tracer's buffer
	// and then being processed.
	span.Finish()
}
