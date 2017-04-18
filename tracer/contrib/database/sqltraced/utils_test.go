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
		"db.user":      "bob",
		"out.host":     "1.2.3.4",
		"out.port":     "5432",
		"db.name":      "mydb",
		"meta.sslmode": "verify-full",
	}
	o, err := parseDSN(&pq.Driver{}, "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, o))

	expected = map[string]string{
		"db.user":  "bob",
		"out.host": "1.2.3.4",
		"out.port": "5432",
		"db.name":  "mydb",
	}
	o, err = parseDSN(&mysql.MySQLDriver{}, "bob:secret@tcp(1.2.3.4:5432)/mydb")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, o))

	expected = map[string]string{
		"meta.binary_parameters": "no",
		"out.port":               "5433",
		"out.host":               "master-db-master-active.postgres.service.consul",
		"meta.connect_timeout":   "0",
		"db.name":                "dogdatastaging",
		"db.application":         "trace-api",
		"meta.sslmode":           "disable",
		"db.user":                "dog",
	}
	dsn := "connect_timeout=0 binary_parameters=no password=zMWmQz26GORmgVVKEbEl dbname=dogdatastaging application_name=trace-api port=5433 sslmode=disable host=master-db-master-active.postgres.service.consul user=dog"
	o, err = parseDSN(&pq.Driver{}, dsn)
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, o))
}
