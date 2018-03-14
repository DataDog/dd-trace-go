package sql_test

import (
	"context"
	"log"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v0/contrib/database/sql"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/tracer"

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
	sqltrace.Register("mysql", &mysql.MySQLDriver{}, sqltrace.WithServiceName("my-db"))

	// Open a connection to the DB using the driver we've just registered with tracing.
	db, err := sqltrace.Open("mysql", "user:password@/dbname")
	if err != nil {
		log.Fatal(err)
	}

	// Create a root span, giving name, server and resource.
	_, ctx := tracer.StartSpanFromContext(context.Background(), "my-query",
		tracer.SpanType(ext.AppTypeDB),
		tracer.ServiceName("my-db"),
		tracer.ResourceName("initial-access"),
	)

	// Subsequent spans inherit their parent from context.
	rows, err := db.QueryContext(ctx, "SELECT * FROM city LIMIT 5")
	if err != nil {
		log.Fatal(err)
	}
	rows.Close()
}
