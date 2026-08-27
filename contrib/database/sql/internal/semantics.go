// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"net"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

// OTelConnectionInfo contains connection attributes used by OpenTelemetry database semantics.
type OTelConnectionInfo struct {
	SystemName    string
	Namespace     string
	ServerAddress string
	ServerPort    int
}

// OTelSemantics returns OpenTelemetry database connection attributes for a driver and parsed DSN.
func (c ConnectionInfo) OTelSemantics(driverName string) OTelConnectionInfo {
	system := otelDBSystem(c.System, driverName)
	info := OTelConnectionInfo{
		SystemName: system,
		Namespace:  c.Namespace,
	}
	if system == ext.DBSystemSQLite {
		return info
	}
	info.ServerAddress = c.Host
	if info.ServerAddress == "" || c.Port == "" || c.Port == defaultPort(system) {
		return info
	}
	port, err := strconv.ParseUint(c.Port, 10, 16)
	if err == nil && port > 0 && defaultPort(system) != "" {
		info.ServerPort = int(port)
	}
	return info
}

// SpanName returns a low-cardinality database span name. Operation may be empty when no reliable
// database operation is available.
func (c OTelConnectionInfo) SpanName(operation string) string {
	target := c.Namespace
	if target == "" {
		target = c.ServerAddress
		if target != "" && c.ServerPort != 0 {
			target = net.JoinHostPort(target, strconv.Itoa(c.ServerPort))
		}
	}
	if target == "" {
		target = c.SystemName
	}
	if operation == "" {
		return target
	}
	return operation + " " + target
}

func otelDBSystem(parsedSystem, driverName string) string {
	system := parsedSystem
	if system == "" {
		system = strings.ToLower(driverName)
	}
	switch system {
	case "mysql":
		return ext.DBSystemMySQL
	case "postgres", "postgresql", "pgx":
		return ext.DBSystemPostgreSQL
	case "sqlserver", "mssql", "azuresql":
		return ext.DBSystemNameMicrosoftSQLServer
	case "sqlite", "sqlite3":
		return ext.DBSystemSQLite
	default:
		return ext.DBSystemOtherSQL
	}
}

func defaultPort(system string) string {
	switch system {
	case ext.DBSystemMySQL:
		return "3306"
	case ext.DBSystemPostgreSQL:
		return "5432"
	case ext.DBSystemNameMicrosoftSQLServer:
		return "1433"
	default:
		return ""
	}
}
