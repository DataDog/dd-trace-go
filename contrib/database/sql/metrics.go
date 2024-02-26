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

var interval = 10 * time.Second

// pollDBStats calls (*DB).Stats on the db at a predetermined interval. It pushes the DBStats off to the StatsCarrier which ultimately sends them through a statsd client.
// TODO: Perhaps grant a way for pollDBStats to grab the drivername so that it doesn't have to be passed in as a param
func pollDBStats(db *sql.DB, tags []string) {
	if db == nil {
		log.Debug("No traced DB connection found; cannot pull DB stats.")
		return
	}
	log.Debug("Traced DB connection found: DB stats will be gathered and sent every %v.", interval)
	for range time.NewTicker(interval).C {
		stat := db.Stats()
		globalconfig.PushStat(internal.NewGauge(MaxOpenConnections, float64(stat.MaxOpenConnections), tags, 1))
		globalconfig.PushStat(internal.NewGauge(OpenConnections, float64(stat.OpenConnections), tags, 1))
		globalconfig.PushStat(internal.NewGauge(InUse, float64(stat.InUse), tags, 1))
		globalconfig.PushStat(internal.NewGauge(Idle, float64(stat.Idle), tags, 1))
		globalconfig.PushStat(internal.NewGauge(WaitCount, float64(stat.WaitCount), tags, 1))
		globalconfig.PushStat(internal.NewTiming(WaitDuration, stat.WaitDuration, tags, 1))
		globalconfig.PushStat(internal.NewGauge(MaxIdleClosed, float64(stat.MaxIdleClosed), tags, 1))
		globalconfig.PushStat(internal.NewGauge(MaxIdleTimeClosed, float64(stat.MaxIdleTimeClosed), tags, 1))
		globalconfig.PushStat(internal.NewGauge(MaxLifetimeClosed, float64(stat.MaxLifetimeClosed), tags, 1))
	}
}
