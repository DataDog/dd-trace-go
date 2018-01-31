package sql

import (
	"log"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/internal/sqltest"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

// tableName holds the SQL table that these tests will be run against. It must be unique cross-repo.
const tableName = "testsql"

func TestMain(m *testing.M) {
	defer sqltest.Prepare(tableName)()
	os.Exit(m.Run())
}

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

	expectedSpan := &tracer.Span{
		Name:    "mysql.query",
		Service: "mysql.db",
		Type:    "sql",
		Meta: map[string]string{
			"db.user":  "test",
			"out.host": "127.0.0.1",
			"out.port": "3306",
			"db.name":  "test",
		},
	}
	testConfig := &sqltest.Config{
		DB:         db,
		Tracer:     trc,
		Transport:  transport,
		DriverName: "mysql",
		TableName:  tableName,
		Expected:   expectedSpan,
	}
	sqltest.RunAll(t, testConfig)
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

	expectedSpan := &tracer.Span{
		Name:    "postgres.query",
		Service: "postgres-test",
		Type:    "sql",
		Meta: map[string]string{
			"db.user":  "postgres",
			"out.host": "127.0.0.1",
			"out.port": "5432",
			"db.name":  "postgres",
		},
	}
	testConfig := &sqltest.Config{
		DB:         db,
		Tracer:     trc,
		Transport:  transport,
		DriverName: "postgres",
		TableName:  tableName,
		Expected:   expectedSpan,
	}
	sqltest.RunAll(t, testConfig)
}
