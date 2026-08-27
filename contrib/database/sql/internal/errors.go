// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/go-sql-driver/mysql"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

// OTelErrorInfo contains error attributes used by OpenTelemetry database semantics.
type OTelErrorInfo struct {
	Type               string
	ResponseStatusCode string
}

// OTelErrorSemantics returns OpenTelemetry error attributes for a failed database operation.
func OTelErrorSemantics(err error, systemName string) OTelErrorInfo {
	if err == nil {
		return OTelErrorInfo{}
	}
	if code := databaseErrorCode(err, systemName); code != "" {
		return OTelErrorInfo{Type: code, ResponseStatusCode: code}
	}
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
		err = cause
	}
	return OTelErrorInfo{Type: fmt.Sprintf("%T", err)}
}

func databaseErrorCode(err error, systemName string) string {
	switch systemName {
	case ext.DBSystemMySQL:
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) {
			return strconv.FormatUint(uint64(mysqlError.Number), 10)
		}
	case ext.DBSystemPostgreSQL:
		var postgresError interface{ SQLState() string }
		if errors.As(err, &postgresError) {
			return postgresError.SQLState()
		}
	case ext.DBSystemNameMicrosoftSQLServer:
		var sqlServerError interface{ SQLErrorNumber() int32 }
		if errors.As(err, &sqlServerError) {
			return strconv.FormatInt(int64(sqlServerError.SQLErrorNumber()), 10)
		}
	}
	return ""
}
