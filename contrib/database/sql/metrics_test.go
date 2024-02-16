// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"
)

// Test that a sql.DBStat is collected and passed off to the pushFn every time pollDBStats is invoked
func TestPollDBStats(t *testing.T) {
	driverName := "postgres"
	assert.Equal(t, "dn", driverName)
	dsn := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	db, err := Open(driverName, dsn)
	require.NoError(t, err)
	interval := 3 * time.Millisecond
	go pollDBStats(interval, db, pollDBStatsCounter)
	time.Sleep(3 * interval)
	assert.Len(t, dbStatsCollector, 3)
}

var dbStatsCollector []sql.DBStats

func pollDBStatsCounter(stats sql.DBStats) {
	fmt.Println("dbstats counter")
	dbStatsCollector = append(dbStatsCollector, stats)
}

// Test that sql.DBStat is converted to statsd payloads and these payloads are pushed through the statsd client every time pushDBStats is invoked
func TestPushDBStats(t *testing.T) {
	driverName := "postgres"
	dsn := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	db, err := Open(driverName, dsn)
	assert.NoError(t, err)
	var tg statsdtest.TestStatsdClient
	sc := internal.NewStatsCarrier(&tg)
	globalconfig.SetStatsCarrier(sc)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pushDBStats(db.Stats())
	}()
	wg.Wait()
	calls := tg.CallNames()
	// As of Feb. 2024, the sql.DBStats type reports 9 distinct metrics
	assert.Len(t, calls, 9)
	assert.Contains(t, calls, MaxOpenConnections)
	assert.Contains(t, calls, OpenConnections)
	assert.Contains(t, calls, InUse)
	assert.Contains(t, calls, Idle)
	assert.Contains(t, calls, WaitCount)
	assert.Contains(t, calls, WaitDuration)
	assert.Contains(t, calls, MaxIdleClosed)
	assert.Contains(t, calls, MaxIdleTimeClosed)
	assert.Contains(t, calls, MaxLifetimeClosed)
}
