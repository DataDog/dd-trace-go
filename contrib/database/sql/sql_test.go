package sql

import (
	"log"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/internal/sqltest"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

func TestMySQL(t *testing.T) {
	originalTracer := tracer.DefaultTracer
	trc, transport := tracertest.GetTestTracer()
	tracer.DefaultTracer = trc
	defer func() {
		tracer.DefaultTracer = originalTracer
	}()
	Register("mysql", &mysql.MySQLDriver{})
	db, err := Open("mysql", "test:test@tcp(127.0.0.1:3306)/test")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testDB := &sqltest.DB{
		DB:         db,
		Tracer:     trc,
		Transport:  transport,
		DriverName: "mysql",
	}
	expectedSpan := &tracer.Span{
		Name:    "mysql.query",
		Service: "mysql.db",
		Type:    "sql",
	}
	expectedSpan.Meta = map[string]string{
		"db.user":  "test",
		"out.host": "127.0.0.1",
		"out.port": "3306",
		"db.name":  "test",
	}

	sqltest.AllSQLTests(t, testDB, expectedSpan)
}

func TestPostgres(t *testing.T) {
	originalTracer := tracer.DefaultTracer
	trc, transport := tracertest.GetTestTracer()
	tracer.DefaultTracer = trc
	defer func() {
		tracer.DefaultTracer = originalTracer
	}()
	RegisterWithServiceName("postgres-test", "postgres", &pq.Driver{})
	db, err := Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	testDB := &sqltest.DB{
		DB:         db,
		Tracer:     trc,
		Transport:  transport,
		DriverName: "postgres",
	}
	expectedSpan := &tracer.Span{
		Name:    "postgres.query",
		Service: "postgres-test",
		Type:    "sql",
	}
	expectedSpan.Meta = map[string]string{
		"db.user":  "postgres",
		"out.host": "127.0.0.1",
		"out.port": "5432",
		"db.name":  "postgres",
	}
	sqltest.AllSQLTests(t, testDB, expectedSpan)
}
