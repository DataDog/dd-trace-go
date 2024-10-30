// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPool(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()

	conn, err := NewPool(ctx, postgresDSN)
	require.NoError(t, err)
	defer conn.Close()

	var x int

	err = conn.QueryRow(ctx, `select 1`).Scan(&x)
	require.NoError(t, err)
	assert.Equal(t, 1, x)

	err = conn.QueryRow(ctx, `select 2`).Scan(&x)
	require.NoError(t, err)
	assert.Equal(t, 2, x)

	assert.Len(t, mt.OpenSpans(), 0)
	assert.Len(t, mt.FinishedSpans(), 7)
}

func TestPoolWithPoolStats(t *testing.T) {
	originalInterval := interval
	interval = 1 * time.Millisecond
	t.Cleanup(func() {
		interval = originalInterval
	})

	ctx := context.Background()
	statsd := new(statsdtest.TestStatsdClient)
	conn, err := NewPool(ctx, postgresDSN, withStatsdClient(statsd), WithPoolStats())
	require.NoError(t, err)
	defer conn.Close()

	wantStats := []string{AcquireCount, AcquireDuration, AcquiredConns, CanceledAcquireCount, ConstructingConns, EmptyAcquireCount, IdleConns, MaxConns, TotalConns, NewConnsCount, MaxLifetimeDestroyCount, MaxIdleDestroyCount}

	assert := assert.New(t)
	if err := statsd.Wait(assert, len(wantStats), time.Second); err != nil {
		t.Fatalf("statsd.Wait(): %v", err)
	}
	for _, name := range wantStats {
		assert.Contains(statsd.CallNames(), name)
	}
}

func withStatsdClient(s internal.StatsdClient) Option {
	return func(c *config) {
		c.statsdClient = s
	}
}
