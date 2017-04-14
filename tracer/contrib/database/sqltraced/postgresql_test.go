package sqltraced

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer/contrib"
	"github.com/lib/pq"
)

func TestPostgres(t *testing.T) {
	// Initializing database
	db := NewDB("Postgres", "postgres-test", &pq.Driver{}, contrib.POSTGRES_CONFIG)

	// Testing MySQL driver
	AllTests(t, db)
}
