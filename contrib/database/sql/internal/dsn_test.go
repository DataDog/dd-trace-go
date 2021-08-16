// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

func TestParseDSN(t *testing.T) {
	assert := assert.New(t)
	for _, tt := range []struct {
		driverName string
		dsn        string
		expected   map[string]string
	}{
		{
			driverName: "postgres",
			dsn:        "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full",
			expected: map[string]string{
				ext.DBUser:     "bob",
				ext.TargetHost: "1.2.3.4",
				ext.TargetPort: "5432",
				ext.DBName:     "mydb",
			},
		},
		{
			driverName: "mysql",
			dsn:        "bob:secret@tcp(1.2.3.4:5432)/mydb",
			expected: map[string]string{
				ext.DBName:     "mydb",
				ext.DBUser:     "bob",
				ext.TargetHost: "1.2.3.4",
				ext.TargetPort: "5432",
			},
		},
		{
			driverName: "postgres",
			dsn:        "connect_timeout=0 binary_parameters=no password=zMWmQz26GORmgVVKEbEl dbname=dogdatastaging application_name=trace-api port=5433 sslmode=disable host=master-db-master-active.postgres.service.consul user=dog",
			expected: map[string]string{
				ext.TargetPort:    "5433",
				ext.TargetHost:    "master-db-master-active.postgres.service.consul",
				ext.DBName:        "dogdatastaging",
				ext.DBApplication: "trace-api",
				ext.DBUser:        "dog",
			},
		},
	} {
		m, err := ParseDSN(tt.driverName, tt.dsn)
		assert.Equal(nil, err)
		assert.Equal(tt.expected, m)
	}
}

func TestParseMySQLDSN(t *testing.T) {
	assert := assert.New(t)
	expected := map[string]string{
		"dbname": "mydb",
		"user":   "bob",
		"host":   "1.2.3.4",
		"port":   "5432",
	}
	m, err := parseMySQLDSN("bob:secret@tcp(1.2.3.4:5432)/mydb")
	assert.Equal(nil, err)
	assert.Equal(expected, m)
}

func TestParsePostgresDSN(t *testing.T) {
	assert := assert.New(t)

	for _, tt := range []struct {
		dsn      string
		expected map[string]string
	}{
		{
			dsn: "postgres://bob:secret@1.2.3.4:5432/mydb?sslmode=verify-full",
			expected: map[string]string{
				"user":    "bob",
				"host":    "1.2.3.4",
				"port":    "5432",
				"dbname":  "mydb",
				"sslmode": "verify-full",
			},
		},
		{
			dsn: "password=zMWmQz26GORmgVVKEbEl dbname=dogdatastaging application_name=trace-api port=5433 host=master-db-master-active.postgres.service.consul user=dog",
			expected: map[string]string{
				"user":             "dog",
				"port":             "5433",
				"host":             "master-db-master-active.postgres.service.consul",
				"dbname":           "dogdatastaging",
				"application_name": "trace-api",
			},
		},
	} {
		m, err := parsePostgresDSN(tt.dsn)
		assert.Equal(nil, err)
		assert.Equal(tt.expected, m)
	}
}
