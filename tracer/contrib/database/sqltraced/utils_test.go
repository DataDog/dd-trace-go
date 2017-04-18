package sqltraced

import (
	"reflect"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestParsesDSN(t *testing.T) {
	assert := assert.New(t)

	expectedPostgres := map[string]string{
		"user":     "bob",
		"password": "secret",
		"host":     "1.2.3.4",
		"port":     "5432",
		"dbname":   "mydb",
		"sslmode":  "verify-full",
	}
	o, err := parseDSN(&pq.Driver{}, "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expectedPostgres, o))

	expectedMySQL := map[string]string{
		"user":   "bob",
		"host":   "1.2.3.4",
		"port":   "5432",
		"dbname": "mydb",
	}
	o, err = parseDSN(&mysql.MySQLDriver{}, "bob:secret@tcp(1.2.3.4:5432)/mydb")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expectedMySQL, o))
}
