// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

func TestOTelSemantics(t *testing.T) {
	tests := []struct {
		name       string
		driverName string
		connection ConnectionInfo
		want       OTelConnectionInfo
	}{
		{
			name:       "mysql default port",
			driverName: "mysql",
			connection: ConnectionInfo{System: "mysql", Namespace: "orders", Host: "db.example.com", Port: "3306"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemMySQL, Namespace: "orders", ServerAddress: "db.example.com"},
		},
		{
			name:       "postgres non-default port",
			driverName: "custom-postgres-alias",
			connection: ConnectionInfo{System: "postgresql", Host: "2001:db8::1", Port: "6432"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemPostgreSQL, ServerAddress: "2001:db8::1", ServerPort: 6432},
		},
		{
			name:       "SQL Server alias",
			driverName: "azuresql",
			connection: ConnectionInfo{Host: "db.example.com", Port: "1434"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemNameMicrosoftSQLServer, ServerAddress: "db.example.com", ServerPort: 1434},
		},
		{
			name:       "SQLite has no server",
			driverName: "sqlite3",
			connection: ConnectionInfo{Namespace: "local.db", Host: "ignored", Port: "1234"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemSQLite, Namespace: "local.db"},
		},
		{
			name:       "unknown driver",
			driverName: "custom",
			connection: ConnectionInfo{Host: "db.example.com", Port: "1234"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemOtherSQL, ServerAddress: "db.example.com"},
		},
		{
			name:       "port requires address",
			driverName: "mysql",
			connection: ConnectionInfo{Port: "3307"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemMySQL},
		},
		{
			name:       "invalid port",
			driverName: "postgres",
			connection: ConnectionInfo{Host: "db.example.com", Port: "invalid"},
			want:       OTelConnectionInfo{SystemName: ext.DBSystemPostgreSQL, ServerAddress: "db.example.com"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.connection.OTelSemantics(test.driverName))
		})
	}
}

func TestOTelSpanName(t *testing.T) {
	tests := []struct {
		name      string
		info      OTelConnectionInfo
		operation string
		want      string
	}{
		{name: "namespace", info: OTelConnectionInfo{SystemName: "mysql", Namespace: "orders", ServerAddress: "host", ServerPort: 3307}, want: "orders"},
		{name: "operation and namespace", info: OTelConnectionInfo{SystemName: "mysql", Namespace: "orders"}, operation: "Ping", want: "Ping orders"},
		{name: "IPv4 server", info: OTelConnectionInfo{SystemName: "postgresql", ServerAddress: "127.0.0.1", ServerPort: 6432}, want: "127.0.0.1:6432"},
		{name: "IPv6 server", info: OTelConnectionInfo{SystemName: "postgresql", ServerAddress: "2001:db8::1", ServerPort: 6432}, want: "[2001:db8::1]:6432"},
		{name: "address without port", info: OTelConnectionInfo{SystemName: "postgresql", ServerAddress: "db.example.com"}, want: "db.example.com"},
		{name: "system fallback", info: OTelConnectionInfo{SystemName: "other_sql"}, want: "other_sql"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.info.SpanName(test.operation))
		})
	}
}
