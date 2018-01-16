package parsedsn

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDSN(t *testing.T) {
	assert := assert.New(t)

	expected := map[string]string{
		"db.user":  "bob",
		"out.host": "1.2.3.4",
		"out.port": "5432",
		"db.name":  "mydb",
	}
	m, err := Parse("postgres", "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, m))

	expected = map[string]string{
		"db.user":  "bob",
		"out.host": "1.2.3.4",
		"out.port": "5432",
		"db.name":  "mydb",
	}
	m, err = Parse("mysql", "bob:secret@tcp(1.2.3.4:5432)/mydb")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, m))

	expected = map[string]string{
		"out.port":       "5433",
		"out.host":       "master-db-master-active.postgres.service.consul",
		"db.name":        "dogdatastaging",
		"db.application": "trace-api",
		"db.user":        "dog",
	}
	dsn := "connect_timeout=0 binary_parameters=no password=zMWmQz26GORmgVVKEbEl dbname=dogdatastaging application_name=trace-api port=5433 sslmode=disable host=master-db-master-active.postgres.service.consul user=dog"
	m, err = Parse("postgres", dsn)
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, m))
}

func TestParseMySQL(t *testing.T) {
	assert := assert.New(t)

	expected := map[string]string{
		"user":   "bob",
		"host":   "1.2.3.4",
		"port":   "5432",
		"dbname": "mydb",
	}
	m, err := ParseMySQL("bob:secret@tcp(1.2.3.4:5432)/mydb")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, m))
}

func TestParsePostgres(t *testing.T) {
	assert := assert.New(t)

	expected := map[string]string{
		"user":    "bob",
		"host":    "1.2.3.4",
		"port":    "5432",
		"dbname":  "mydb",
		"sslmode": "verify-full",
	}
	m, err := ParsePostgres("postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full")
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, m))

	expected = map[string]string{
		"user":             "dog",
		"port":             "5433",
		"host":             "master-db-master-active.postgres.service.consul",
		"dbname":           "dogdatastaging",
		"application_name": "trace-api",
	}
	dsn := "password=zMWmQz26GORmgVVKEbEl dbname=dogdatastaging application_name=trace-api port=5433 host=master-db-master-active.postgres.service.consul user=dog"
	m, err = ParsePostgres(dsn)
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expected, m))
}
