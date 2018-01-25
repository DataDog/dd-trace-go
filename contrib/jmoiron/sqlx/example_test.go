package sqlx_test

import (
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	sqlxtrace "github.com/DataDog/dd-trace-go/contrib/jmoiron/sqlx"
)

// The API to trace sqlx calls is the same as sqltraced.
// See https://godoc.org/github.com/DataDog/dd-trace-go/contrib/database/sql for more information on how to use it.
func Example() {
	// OpenTraced will first register a traced version of the driver and then will return the sqlx.DB object
	// that holds the connection with the database.
	// The third argument is used to specify the name of the service under which traces will appear in the Datadog app.
	sqlxtrace.Register("postgres", &pq.Driver{})
	db, _ := sqlxtrace.Open("postgres", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")

	// All calls through sqlx API will then be traced.
	query, args, _ := sqlx.In("SELECT * FROM users WHERE level IN (?);", []int{4, 6, 7})
	query = db.Rebind(query)
	rows, _ := db.Queryx(query, args...)
	defer rows.Close()
}
