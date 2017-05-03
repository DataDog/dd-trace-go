package sqltraced_test

import (
	"context"
	"log"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/lib/pq"
)

func Example() {
	// First register a traced version of your driver of choice.
	sqltraced.Register("postgres", &pq.Driver{})

	// Open a connection to your database, passing in a service name
	// that identifies your DB
	db, _ := sqltraced.Open("postgres", test.PostgresConfig.DSN(), "web-backend")
	defer db.Close()

	// Now use the connection as usual
	age := 27
	rows, err := db.Query("SELECT name FROM users WHERE age=?", age)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
}

func Example_context() {
	// Register and Open a connection with sqltraced
	sqltraced.Register("postgres", &pq.Driver{})
	db, _ := sqltraced.Open("postgres", test.PostgresConfig.DSN(), "web-backend")
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
