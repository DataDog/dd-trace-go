package sqltraced_test

import (
	"context"
	"log"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

// To trace the sql calls, you just need to open your sql.DB with OpenTraced.
// All calls through this sql.DB object will then be traced.
func Example() {
	// OpenTraced will first register a traced version of the postgres driver and then will return the
	// connection with it. The third argument is used to specify the name of the service
	// under which traces will appear in the Datadog app.
	db, err := sqltraced.OpenTraced(&pq.Driver{}, "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable", "web-backend")
	if err != nil {
		log.Fatal(err)
	}

	// All calls through the database/sql API will then be traced.
	rows, err := db.Query("SELECT name FROM users WHERE age=?", 27)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
}

// If you want to link your db calls with existing traces, you need to use
// the context version of the database/sql API.
// Just make sure you are passing the parent span within the context.
func Example_context() {
	// OpenTraced will first register a traced version of the postgres driver and then will return the
	// connection with it. The third argument is used to specify the name of the service
	// under which traces will appear in the Datadog app.
	db, err := sqltraced.OpenTraced(&pq.Driver{}, "user:password@/dbname", "web-backend")
	if err != nil {
		log.Fatal(err)
	}

	// We create a parent span and put it within the context.
	span := tracer.NewRootSpan("postgres.parent", "web-backend", "query-parent")
	ctx := tracer.ContextWithSpan(context.Background(), span)

	// We need to use the context version of the database/sql API
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

// You can trace all drivers implementing the database/sql/driver interface.
// For example, you can trace the go-sql-driver/mysql with the following code.
func Example_mySQL() {
	// OpenTraced will first register a traced version of the mysql driver and then will return the
	// connection with it. The third argument is used to specify the name of the service
	// under which traces will appear in the Datadog app.
	db, err := sqltraced.OpenTraced(&mysql.MySQLDriver{}, "user:password@/dbname", "web-backend")
	if err != nil {
		log.Fatal(err)
	}

	// All calls through the database/sql API will then be traced.
	rows, err := db.Query("SELECT name FROM users WHERE age=?", 27)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
}

// If you need more granularity, you can register the traced driver seperately from the Open call.
// You can even use a custom tracer as an optional argument of Register.
func ExampleRegister() {
	// Register a traced version of your driver of choice.
	sqltraced.Register("postgres", &pq.Driver{})

	// Open a connection to your database, passing in a service name
	// that identifies your DB.
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
