package sqltraced

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib"
	"github.com/go-sql-driver/mysql"
)

func TestMySQL(t *testing.T) {
	// Initializing mysql database
	db := NewDB("MySQL", "mysql-test", &mysql.MySQLDriver{}, contrib.MYSQL_CONFIG)
	defer db.Close()

	// Expected span
	expectedSpan := tracer.Span{
		Name:    "mysql.",
		Service: "mysql-test",
		Type:    "sql",
	}
	expectedSpan.Meta = map[string]string{
		"out.host": db.Host,
		"out.port": db.Port,
		"db.name":  db.DBName,
		"db.user":  db.User,
	}

	// Testing MySQL driver
	AllTests(t, db, expectedSpan)
}
