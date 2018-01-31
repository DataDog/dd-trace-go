package sql_test

import (
	"context"
	"log"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

func Example() {
	// The first step is to register the driver that we will be using.
	sqltrace.Register("postgres", &pq.Driver{})

	// Followed by a call to Open.
	db, err := sqltrace.Open("postgres", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}

	// Then, we continue using the database/sql package as we normally would, with tracing.
	rows, err := db.Query("SELECT name FROM users WHERE age=?", 27)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
}

func Example_context() {
	// Register the driver that we will be using (in this case mysql) under a custom service name.
	sqltrace.RegisterWithServiceName("my-db", "mysql", &mysql.MySQLDriver{})

	// Open a connection to the DB using the driver we've just registered with tracing.
	db, err := sqltrace.Open("mysql", "user:password@/dbname")
	if err != nil {
		log.Fatal(err)
	}

	// Create a root span, giving name, server and resource.
	span := tracer.NewRootSpan("my-query", "my-db", "initial-access")

	// Create a context containing the span.
	ctx := tracer.ContextWithSpan(context.Background(), span)

	// Ssubsequent spans inherit their parent from context.
	rows, err := db.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	if err != nil {
		log.Fatal(err)
	}
	rows.Close()
}
