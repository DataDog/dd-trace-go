package sqlxtraced

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
	"github.com/go-sql-driver/mysql"
)

func TestMySQL(t *testing.T) {
	// Initializing mysql database
	dsn := "ubuntu@tcp(127.0.0.1:3306)/circle_test"
	db := newDB("mysql", "mysql-test", &mysql.MySQLDriver{}, dsn)
	defer db.Close()

	// Expected span
	expectedSpan := &tracer.Span{
		Name:    "mysql.query",
		Service: "mysql-test",
		Type:    "sql",
	}
	expectedSpan.Meta = map[string]string{
		"db.user":  "ubuntu",
		"out.host": "127.0.0.1",
		"out.port": "3306",
		"db.name":  "circle_test",
	}

	// Testing MySQL driver
	sqltraced.AllSQLTests(t, db, expectedSpan)
}
