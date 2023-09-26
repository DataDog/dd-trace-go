// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
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
		{
			driverName: "sqlserver",
			dsn:        "sqlserver://bob:secret@1.2.3.4:1433?database=mydb",
			expected: map[string]string{
				ext.DBUser:     "bob",
				ext.TargetHost: "1.2.3.4",
				ext.TargetPort: "1433",
				ext.DBName:     "mydb",
			},
		},
		{
			driverName: "sqlserver",
			dsn:        "sqlserver://alice:secret@localhost/SQLExpress?database=mydb",
			expected: map[string]string{
				ext.DBUser:                         "alice",
				ext.TargetHost:                     "localhost",
				ext.DBName:                         "mydb",
				ext.MicrosoftSQLServerInstanceName: "SQLExpress",
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

func TestParseSqlServerDSN(t *testing.T) {
	for _, tt := range []struct {
		name     string
		dsn      string
		expected map[string]string
	}{
		{
			name: "sqlserver_url_1",
			dsn:  "sqlserver://bob:secret@1.2.3.4:1433?database=mydb",
			expected: map[string]string{
				"user":   "bob",
				"host":   "1.2.3.4",
				"port":   "1433",
				"dbname": "mydb",
			},
		},
		{
			name: "sqlserver_url_2",
			dsn:  "sqlserver://alice:secret@localhost/SQLExpress?database=mydb",
			expected: map[string]string{
				"user":                   "alice",
				"host":                   "localhost",
				"dbname":                 "mydb",
				"db.mssql.instance_name": "SQLExpress",
			},
		},
		{
			name: "ado_1",
			dsn:  "server=1.2.3.4,1433;User Id=dog;Password=secret;Database=mydb;",
			expected: map[string]string{
				"user":   "dog",
				"port":   "1433",
				"host":   "1.2.3.4",
				"dbname": "mydb",
			},
		},
		{
			name: "ado_2",
			dsn:  "ADDRESS=1.2.3.4;UID=cat;PASSWORD=secret;INITIAL CATALOG=mydb;",
			expected: map[string]string{
				"user":   "cat",
				"host":   "1.2.3.4",
				"dbname": "mydb",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseSQLServerDSN(tt.dsn)
			assert.Equal(t, nil, err)
			assert.Equal(t, tt.expected, m)
		})
	}
}
