package sqltraced

import (
	"log"
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced/sqltest"
	"github.com/DataDog/dd-trace-go/tracer/tracertest"
	"github.com/lib/pq"
)

func TestPostgres(t *testing.T) {
	trc, transport := tracertest.GetTestTracer()
	db, err := OpenTraced(&pq.Driver{}, "postgres://ubuntu@127.0.0.1:5432/circle_test?sslmode=disable", "postgres-test", trc)
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
		"db.user":  "ubuntu",
		"out.host": "127.0.0.1",
		"out.port": "5432",
		"db.name":  "circle_test",
	}
	for k, v := range trc.GetAllMeta() {
		expectedSpan.SetMeta(k, v)
	}

	sqltest.AllSQLTests(t, testDB, expectedSpan)
}
