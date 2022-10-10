// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext

const (

	// ClickHouseQuery is the tag name used for ClickHouse queries.
	ClickHouseQuery = "clickhouse.query"

	// ClickHouseBatch is the tag name used for ClickHouse batches.
	ClickHouseBatch = "clickhouse.batch"

	// ClickHouseConnectionOpen specifies the tag name to use when WithStats() option is enabled.
	ClickHouseConnectionOpen = "clickhouse.connection.open"

	// ClickHouseConnectionIdle specifies the tag name to use when WithStats() option is enabled.
	ClickHouseConnectionIdle = "clickhouse.connection.idle"

	// ClickHouseMaxOpenConnections specifies the tag name to use when WithStats() option is enabled.
	ClickHouseMaxOpenConnections = "clickhouse.connection.maxOpen"

	// ClickHouseMaxIdleConnections specifies the tag name to use when WithStats() option is enabled.
	ClickHouseMaxIdleConnections = "clickhouse.connection.maxIdle"
)
