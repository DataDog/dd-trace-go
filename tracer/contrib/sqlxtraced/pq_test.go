package sqlxtraced

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
	"github.com/lib/pq"
)

func TestPostgres(t *testing.T) {
	// Initializing database
	dsn := "postgres://ubuntu@127.0.0.1:5432/circle_test?sslmode=disable"
	db := NewDB("postgres", "postgres-test", &pq.Driver{}, dsn)
	defer db.Close()

	// Expected span
	expectedSpan := &tracer.Span{
		Name:    "postgres.query",
		Service: "postgres-test",
		Type:    "sql",
	}
	expectedSpan.Meta = map[string]string{
		"db.user":  "ubuntu",
		"out.host": "127.0.0.1",
		"out.port": "5432",
		"db.name":  "circle_test",
	}

	// Testing MySQL driver
	sqltraced.AllSQLTests(t, db, expectedSpan)
}
