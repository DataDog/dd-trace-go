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

	expected := map[string]string{
		"user":    "bob",
		"host":    "1.2.3.4",
		"port":    "5432",
		"dbname":  "mydb",
		"sslmode": "verify-full",
	}
	o, err := parseDSN(&pq.Driver{}, "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, o))

	expected = map[string]string{
		"user":   "bob",
		"host":   "1.2.3.4",
		"port":   "5432",
		"dbname": "mydb",
	}
	o, err = parseDSN(&mysql.MySQLDriver{}, "bob:secret@tcp(1.2.3.4:5432)/mydb")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, o))

	expected = map[string]string{
		"binary_parameters": "no",
		"port":              "5433",
		"host":              "master-db-master-active.postgres.service.consul",
		"connect_timeout":   "0",
		"dbname":            "dogdatastaging",
		"application_name":  "trace-api",
		"sslmode":           "disable",
		"user":              "dog",
	}
	dsn := "connect_timeout=0 binary_parameters=no password=zMWmQz26GORmgVVKEbEl dbname=dogdatastaging application_name=trace-api port=5433 sslmode=disable host=master-db-master-active.postgres.service.consul user=dog"
	o, err = parseDSN(&pq.Driver{}, dsn)
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, o))
}
