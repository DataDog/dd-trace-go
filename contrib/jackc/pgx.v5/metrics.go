// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

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

var interval = 10 * time.Second

// pollPoolStats calls (*pgxpool).Stats on the pool at a predetermined interval. It pushes the pool Stats off to the statsd client.
func pollPoolStats(statsd internal.StatsdClient, pool *pgxpool.Pool) {
	log.Debug("contrib/jackc/pgx.v5: Traced pool connection found: Pool stats will be gathered and sent every %v.", interval)
	for range time.NewTicker(interval).C {
		log.Debug("contrib/jackc/pgx.v5: Reporting pgxpool.Stat metrics...")
		stat := pool.Stat()
		statsd.Gauge(AcquireCount, float64(stat.AcquireCount()), []string{}, 1)
		statsd.Timing(AcquireDuration, stat.AcquireDuration(), []string{}, 1)
		statsd.Gauge(AcquiredConns, float64(stat.AcquiredConns()), []string{}, 1)
		statsd.Gauge(CanceledAcquireCount, float64(stat.CanceledAcquireCount()), []string{}, 1)
		statsd.Gauge(ConstructingConns, float64(stat.ConstructingConns()), []string{}, 1)
		statsd.Gauge(EmptyAcquireCount, float64(stat.EmptyAcquireCount()), []string{}, 1)
		statsd.Gauge(IdleConns, float64(stat.IdleConns()), []string{}, 1)
		statsd.Gauge(MaxConns, float64(stat.MaxConns()), []string{}, 1)
		statsd.Gauge(TotalConns, float64(stat.TotalConns()), []string{}, 1)
		statsd.Gauge(NewConnsCount, float64(stat.NewConnsCount()), []string{}, 1)
		statsd.Gauge(MaxLifetimeDestroyCount, float64(stat.MaxLifetimeDestroyCount()), []string{}, 1)
		statsd.Gauge(MaxIdleDestroyCount, float64(stat.MaxIdleDestroyCount()), []string{}, 1)
	}
}

func statsTags(c *config) []string {
	tags := globalconfig.StatsTags()
	if c.serviceName != "" {
		tags = append(tags, "service:"+c.serviceName)
	}
	return tags
}
