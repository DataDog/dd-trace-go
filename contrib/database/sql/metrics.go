// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"

import (
	"database/sql"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const tracerPrefix = "datadog.tracer."

// ref: https://pkg.go.dev/database/sql#DBStats
const (
	MaxOpenConnections = tracerPrefix + "sql.db.connections.max_open"
	OpenConnections    = tracerPrefix + "sql.db.connections.open"
	InUse              = tracerPrefix + "sql.db.connections.in_use"
	Idle               = tracerPrefix + "sql.db.connections.idle"
	WaitCount          = tracerPrefix + "sql.db.connections.waiting"
	WaitDuration       = tracerPrefix + "sql.db.connections.wait_duration"
	MaxIdleClosed      = tracerPrefix + "sql.db.connections.closed.max_idle_conns"
	MaxIdleTimeClosed  = tracerPrefix + "sql.db.connections.closed.max_idle_time"
	MaxLifetimeClosed  = tracerPrefix + "sql.db.connections.closed.max_lifetime"
)

// pollDBStats calls (*DB).Stats on the db, at the specified interval. It pushes the DBStats off to the StatsCarrier
func pollDBStats(interval time.Duration, db *sql.DB) {
	if db == nil {
		log.Debug("No traced DB connection found; cannot pull DB stats.")
		return
	}
	log.Debug("Traced DB connection found: DB stats will be gathered and sent every %v.", interval)
	for range time.NewTicker(interval).C {
		stat := db.Stats()
		globalconfig.PushStat(internal.NewGauge(MaxOpenConnections, float64(stat.MaxOpenConnections), nil, 1))
		globalconfig.PushStat(internal.NewGauge(OpenConnections, float64(stat.OpenConnections), nil, 1))
		globalconfig.PushStat(internal.NewGauge(InUse, float64(stat.InUse), nil, 1))
		globalconfig.PushStat(internal.NewGauge(Idle, float64(stat.Idle), nil, 1))
		globalconfig.PushStat(internal.NewGauge(WaitCount, float64(stat.WaitCount), nil, 1))
		globalconfig.PushStat(internal.NewTiming(WaitDuration, stat.WaitDuration, nil, 1))
		globalconfig.PushStat(internal.NewCount(MaxIdleClosed, int64(stat.MaxIdleClosed), nil, 1))
		globalconfig.PushStat(internal.NewCount(MaxIdleTimeClosed, int64(stat.MaxIdleTimeClosed), nil, 1))
		globalconfig.PushStat(internal.NewCount(MaxLifetimeClosed, int64(stat.MaxLifetimeClosed), nil, 1))
	}
}