package sqltraced

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer/contrib"
	"github.com/go-sql-driver/mysql"
)

func TestMySQL(t *testing.T) {
	// Initializing mysql database
	db := NewDB("MySQL", "mysql-test", &mysql.MySQLDriver{}, contrib.MYSQL_CONFIG)
	defer db.Close()

	// Testing MySQL driver
	AllTests(t, db)
}
