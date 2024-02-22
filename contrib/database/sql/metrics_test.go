// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"
)

// TestPollDBStats tests that pollDBStats collects DBStat data at the specified interval and passes all 9 statsd payloads up to the statsd client
func TestPollDBStats(t *testing.T) {
	driverName := "postgres"
	dsn := "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	db, err := Open(driverName, dsn)
	require.NoError(t, err)

	var tg statsdtest.TestStatsdClient
	sc := internal.NewStatsCarrier(&tg)
	sc.Start()
	defer sc.Stop()
	globalconfig.SetStatsCarrier(sc)
	go func() {
		pollDBStats(2*time.Millisecond, db)
	}()
	time.Sleep(5 * time.Millisecond)
	calls := tg.CallNames()
	assert.Len(t, calls, 18)
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