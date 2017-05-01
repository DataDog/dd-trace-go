package sqltraced

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/test"
	"github.com/lib/pq"
)

func TestPostgres(t *testing.T) {
	// Initializing database
	db := NewDB("Postgres", "postgres-test", &pq.Driver{}, test.PostgresConfig)
	defer db.Close()

	// Expected span
	expectedSpan := &tracer.Span{
		Name:    "postgres.query",
		Service: "postgres-test",
		Type:    "sql",
	}
	expectedSpan.Meta = map[string]string{
		"out.host":     db.Host,
		"out.port":     db.Port,
		"db.name":      db.DBName,
		"db.user":      db.User,
		"meta.sslmode": "disable",
	}

	// Testing MySQL driver
	AllTests(t, db, expectedSpan)
}
