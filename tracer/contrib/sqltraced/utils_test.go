package sqltraced

import (
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestStringInSlice(t *testing.T) {
	assert := assert.New(t)

	list := []string{"mysql", "postgres", "pq"}
	assert.True(stringInSlice(list, "pq"))
	assert.False(stringInSlice(list, "Postgres"))
}

func TestGetDriverName(t *testing.T) {
	assert := assert.New(t)

	assert.Equal("postgres", GetDriverName(&pq.Driver{}))
	assert.Equal("mysql", GetDriverName(&mysql.MySQLDriver{}))
	assert.Equal("", GetDriverName(nil))
}

func TestDNSAndService(t *testing.T) {
	assert := assert.New(t)

	dns := "postgres://ubuntu@127.0.0.1:5432/circle_test?sslmode=disable"
	service := "master-db"

	dnsAndService := "postgres://ubuntu@127.0.0.1:5432/circle_test?sslmode=disable|master-db"
	assert.Equal(dnsAndService, newDNSAndService(dns, service))

	actualDNS, actualService := parseDNSAndService(dnsAndService)
	assert.Equal(dns, actualDNS)
	assert.Equal(service, actualService)
}
