// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"errors"
	"fmt"
	"testing"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

type postgresTestError struct {
	state string
}

func (e postgresTestError) Error() string    { return "postgres error" }
func (e postgresTestError) SQLState() string { return e.state }

type unknownTestError struct{}

func (unknownTestError) Error() string { return "unknown error" }

func TestOTelErrorSemantics(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		systemName string
		want       OTelErrorInfo
	}{
		{
			name:       "nil",
			systemName: ext.DBSystemOtherSQL,
			want:       OTelErrorInfo{},
		},
		{
			name:       "MySQL vendor code",
			err:        &mysql.MySQLError{Number: 1062, Message: "duplicate entry"},
			systemName: ext.DBSystemMySQL,
			want:       OTelErrorInfo{Type: "1062", ResponseStatusCode: "1062"},
		},
		{
			name:       "wrapped PostgreSQL SQLSTATE",
			err:        fmt.Errorf("query failed: %w", &pq.Error{Code: pq.ErrorCode("23505")}),
			systemName: ext.DBSystemPostgreSQL,
			want:       OTelErrorInfo{Type: "23505", ResponseStatusCode: "23505"},
		},
		{
			name:       "wrapped SQL Server number",
			err:        fmt.Errorf("query failed: %w", mssql.Error{Number: 2627}),
			systemName: ext.DBSystemNameMicrosoftSQLServer,
			want:       OTelErrorInfo{Type: "2627", ResponseStatusCode: "2627"},
		},
		{
			name:       "empty PostgreSQL SQLSTATE",
			err:        postgresTestError{},
			systemName: ext.DBSystemPostgreSQL,
			want:       OTelErrorInfo{Type: "internal.postgresTestError"},
		},
		{
			name:       "driver mismatch",
			err:        &mysql.MySQLError{Number: 1062, Message: "duplicate entry"},
			systemName: ext.DBSystemOtherSQL,
			want:       OTelErrorInfo{Type: "*mysql.MySQLError"},
		},
		{
			name:       "unwraps unknown error",
			err:        fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", unknownTestError{})),
			systemName: ext.DBSystemOtherSQL,
			want:       OTelErrorInfo{Type: "internal.unknownTestError"},
		},
		{
			name:       "standard error",
			err:        errors.New("failure"),
			systemName: ext.DBSystemOtherSQL,
			want:       OTelErrorInfo{Type: "*errors.errorString"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, OTelErrorSemantics(tt.err, tt.systemName))
		})
	}
}
