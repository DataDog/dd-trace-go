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

// pollDBStats calls (*DB).Stats on the db, at the specified interval. It pushes the DBStat off to the pushFn.
func pollDBStats(interval time.Duration, db *sql.DB, pushFn func(stat sql.DBStats)) {
	for range time.NewTicker(interval).C {
		if db == nil {
			log.Debug("No traced DB connection found; cannot pull DB stats.")
			return
		}
		log.Debug("Traced DB connection found: DB stats will be gathered and sent every %v.", interval)
		pushFn(db.Stats())
	}
}

// pushDBStats separates the DBStats type out into individual statsd payloads and submits to the globalconfig's statsd client
func pushDBStats(stat sql.DBStats) {
	// TODO: ADD TAGS
	// TODO: MAYBE NOT ALL GAUGES ?
	globalconfig.PushStat(internal.NewGauge(MaxOpenConnections, float64(stat.MaxOpenConnections), nil, 1))
	globalconfig.PushStat(internal.NewGauge(OpenConnections, float64(stat.OpenConnections), nil, 1))
	globalconfig.PushStat(internal.NewGauge(InUse, float64(stat.InUse), nil, 1))
	globalconfig.PushStat(internal.NewGauge(WaitCount, float64(stat.WaitCount), nil, 1))
	globalconfig.PushStat(internal.NewTiming(WaitDuration, stat.WaitDuration, nil, 1))
	globalconfig.PushStat(internal.NewGauge(MaxIdleClosed, float64(stat.MaxIdleClosed), nil, 1))
	globalconfig.PushStat(internal.NewGauge(MaxIdleTimeClosed, float64(stat.MaxIdleTimeClosed), nil, 1))
	globalconfig.PushStat(internal.NewGauge(MaxLifetimeClosed, float64(stat.MaxLifetimeClosed), nil, 1))

}
