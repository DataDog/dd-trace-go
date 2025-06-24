// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"errors"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

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

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		// Nil error handling
		{
			name:     "nil_error",
			err:      nil,
			expected: "",
		},

		// URL error type detection
		{
			name:     "url_error_with_password",
			err:      &url.Error{Op: "parse", URL: "postgres://user:secret123@localhost:5432/db", Err: errors.New("invalid port")},
			expected: `parse "postgres://user:xxxxx@localhost:5432/db": invalid port`,
		},

		// General error sanitization (non-URL errors)
		{
			name:     "mysql_dsn_error_with_password",
			err:      errors.New("invalid DSN: user:secretpass@tcp(localhost:3306)/database"),
			expected: "invalid DSN: user:xxxxx@tcp(localhost:3306)/database",
		},
		{
			name:     "key_value_password",
			err:      errors.New("pq: password authentication failed with password=MySecretPass123 host=localhost"),
			expected: "pq: password authentication failed with password=xxxxx host=localhost",
		},
		{
			name:     "mixed_format_error",
			err:      errors.New("tried postgres://user:pass123@host/db and password=pass456 but failed"),
			expected: "tried postgres://user:xxxxx@host/db and password=xxxxx but failed",
		},

		// Errors without sensitive data (should remain unchanged)
		{
			name:     "no_sensitive_data",
			err:      errors.New("connection timeout: unable to reach server at localhost:5432"),
			expected: "connection timeout: unable to reach server at localhost:5432",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeError(tt.err)
			if tt.err == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.expected, result.Error())
			}
		})
	}
}

func TestParseDSNErrorHandling(t *testing.T) {
	testCases := []struct {
		name       string
		driverName string
		dsn        string
	}{
		{
			name:       "invalid_mysql_dsn",
			driverName: "mysql",
			dsn:        "user:pass@invalid@tcp(host:port)/db",
		},
		{
			name:       "unknown_driver_with_valid_url",
			driverName: "unknown",
			dsn:        "postgres://user:pass@host:5432/db",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				ParseDSN(tc.driverName, tc.dsn)
			})
		})
	}
}

func TestParseSafe(t *testing.T) {
	tests := []struct {
		name        string
		rawURL      string
		expectError bool
		expectURL   bool
		expectedErr string
	}{
		// Successful parsing cases
		{
			name:        "valid_url_with_credentials",
			rawURL:      "postgres://user:pass@localhost:5432/db",
			expectError: false,
			expectURL:   true,
		},

		// Error sanitization test
		{
			name:        "invalid_port_with_credentials",
			rawURL:      "postgres://user:secret123@localhost:invalid_port/db",
			expectError: true,
			expectURL:   false,
			expectedErr: `parse "postgres://user:xxxxx@localhost:invalid_port/db": invalid port ":invalid_port" after host`,
		},

		// Non-credential error (should remain unchanged)
		{
			name:        "invalid_port_no_credentials",
			rawURL:      "postgres://localhost:invalid_port/db",
			expectError: true,
			expectURL:   false,
			expectedErr: `parse "postgres://localhost:invalid_port/db": invalid port ":invalid_port" after host`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseSafe(tt.rawURL)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
				if tt.expectedErr != "" {
					assert.Equal(t, tt.expectedErr, err.Error())
				}
			} else {
				assert.NoError(t, err)
				if tt.expectURL {
					assert.NotNil(t, result)
				}
			}
		})
	}
}

// Test individual sanitization functions directly
func TestSanitizeKeyValuePasswords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "space_separated_password",
			input:    "password=secret123 host=localhost",
			expected: "password=xxxxx host=localhost",
		},
		{
			name:     "semicolon_separated_password",
			input:    "Server=host;Password=secret;Database=db;",
			expected: "Server=host;Password=xxxxx;Database=db;",
		},
		{
			name:     "case_insensitive",
			input:    "PASSWORD=secret PWD=secret2",
			expected: "PASSWORD=xxxxx PWD=xxxxx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeKeyValuePasswords(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeURLPasswords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple_url_password",
			input:    "postgres://user:secret@localhost:5432/db",
			expected: "postgres://user:xxxxx@localhost:5432/db",
		},
		{
			name:     "complex_password_with_special_chars",
			input:    "mysql://admin:p@$$w0rd@host.com/database",
			expected: "mysql://admin:xxxxx@host.com/database",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeURLPasswords(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeMySQLPasswords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "mysql_dsn_format",
			input:    "Error connecting: user:password@tcp(localhost:3306)/db",
			expected: "Error connecting: user:xxxxx@tcp(localhost:3306)/db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeMySQLPasswords(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeURLError(t *testing.T) {
	tests := []struct {
		name     string
		urlError *url.Error
		expected string
	}{
		{
			name: "parseable_url_with_password",
			urlError: &url.Error{
				Op:  "parse",
				URL: "postgres://user:secret@localhost:5432/db",
				Err: errors.New("test error"),
			},
			expected: `parse "postgres://user:xxxxx@localhost:5432/db": test error`,
		},
		{
			name: "unparseable_url_fallback",
			urlError: &url.Error{
				Op:  "parse",
				URL: "postgres://user:pass@host with spaces/db",
				Err: errors.New("invalid character in host name"),
			},
			expected: `parse "postgres://user:xxxxx@host with spaces/db": invalid character in host name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeURLError(tt.urlError)
			assert.Equal(t, tt.expected, result.Error())
		})
	}
}
