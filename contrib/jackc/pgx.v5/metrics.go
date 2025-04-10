// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

const tracerPrefix = "datadog.tracer."

const (
	AcquireCount            = tracerPrefix + "pgx.pool.connections.acquire"
	AcquireDuration         = tracerPrefix + "pgx.pool.connections.acquire_duration"
	AcquiredConns           = tracerPrefix + "pgx.pool.connections.acquired_conns"
	CanceledAcquireCount    = tracerPrefix + "pgx.pool.connections.canceled_acquire"
	ConstructingConns       = tracerPrefix + "pgx.pool.connections.constructing_conns"
	EmptyAcquireCount       = tracerPrefix + "pgx.pool.connections.empty_acquire"
	IdleConns               = tracerPrefix + "pgx.pool.connections.idle_conns"
	MaxConns                = tracerPrefix + "pgx.pool.connections.max_conns"
	TotalConns              = tracerPrefix + "pgx.pool.connections.total_conns"
	NewConnsCount           = tracerPrefix + "pgx.pool.connections.new_conns"
	MaxLifetimeDestroyCount = tracerPrefix + "pgx.pool.connections.max_lifetime_destroy"
	MaxIdleDestroyCount     = tracerPrefix + "pgx.pool.connections.max_idle_destroy"
)
