package sqltraced

import (
	"reflect"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestParseDSN(t *testing.T) {
	assert := assert.New(t)

	o, err := parseDSN(pq.Driver{}, "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full")
	expectedO := map[string]string{
		"user":     "bob",
		"password": "secret",
		"host":     "1.2.3.4",
		"port":     "5432",
		"dbname":   "mydb",
		"sslmode":  "verify-full",
	}

	for key, value := range o {
		println("Key:", key, "Value:", value)
	}
	assert.Equal(nil, err)
	assert.True(reflect.DeepEqual(expectedO, o))
}
