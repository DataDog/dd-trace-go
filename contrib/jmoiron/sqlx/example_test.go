package sqlx_test

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	sqlxtrace "github.com/DataDog/dd-trace-go/contrib/jmoiron/sqlx"
)

func Example() {
	// Register informs the sqlxtrace package of the driver that we will be using in our program.
	// It uses a default service name, in the below case "postgres.db". To use a custom service
	// name use RegisterWithServiceName.
	sqlxtrace.Register("postgres", &pq.Driver{})
	db, err := sqlxtrace.Open("postgres", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}

	// All calls through sqlx API will then be traced.
	query, args, err := sqlx.In("SELECT * FROM users WHERE level IN (?);", []int{4, 6, 7})
	if err != nil {
		log.Fatal(err)
	}
	query = db.Rebind(query)
	rows, err := db.Queryx(query, args...)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
}
