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

// pollDBStats calls (*DB).Stats on the db at a predetermined interval. It pushes the DBStats off to the statsd client.
// the caller should always ensure that db & statsd are non-nil
func pollDBStats(statsd internal.StatsdClient, db *sql.DB) {
	log.Debug("DB stats will be gathered and sent every %v.", interval)
	for range time.NewTicker(interval).C {
		log.Debug("Reporting DB.Stats metrics...")
		stat := db.Stats()
		statsd.Gauge(MaxOpenConnections, float64(stat.MaxOpenConnections), []string{}, 1)
		statsd.Gauge(OpenConnections, float64(stat.OpenConnections), []string{}, 1)
		statsd.Gauge(InUse, float64(stat.InUse), []string{}, 1)
		statsd.Gauge(Idle, float64(stat.Idle), []string{}, 1)
		statsd.Gauge(WaitCount, float64(stat.WaitCount), []string{}, 1)
		statsd.Timing(WaitDuration, stat.WaitDuration, []string{}, 1)
		statsd.Gauge(MaxIdleClosed, float64(stat.MaxIdleClosed), []string{}, 1)
		statsd.Gauge(MaxIdleTimeClosed, float64(stat.MaxIdleTimeClosed), []string{}, 1)
		statsd.Gauge(MaxLifetimeClosed, float64(stat.MaxLifetimeClosed), []string{}, 1)
	}
}

func statsTags(c *config) []string {
	tags := globalconfig.StatsTags()
	if c.serviceName != "" {
		tags = append(tags, "service:"+c.serviceName)
	}
	// TODO: grab tracer config's env and hostname for globaltags
	for k, v := range c.tags {
		if vstr, ok := v.(string); ok {
			tags = append(tags, k+":"+vstr)
		}
	}
	return tags
}
