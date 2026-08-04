// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

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
	wantStats := []string{AcquireCount, AcquireDuration, AcquiredConns, CanceledAcquireCount, ConstructingConns, EmptyAcquireCount, IdleConns, MaxConns, TotalConns, NewConnsCount, MaxLifetimeDestroyCount, MaxIdleDestroyCount}

	testCases := []struct {
		name     string
		opts     []Option
		wantTags []string
	}{
		{
			name:     "no pool name",
			opts:     []Option{},
			wantTags: []string{"pool_name:127.0.0.1:5432/postgres"},
		},
		{
			name:     "explicit pool name",
			opts:     []Option{WithPoolName("test-pool"), WithService("test-service")},
			wantTags: []string{"pool_name:test-pool", "service:test-service"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalInterval := interval
			interval = 1 * time.Millisecond
			t.Cleanup(func() {
				interval = originalInterval
			})

			ctx := context.Background()
			statsd := testutils.NewMockStatsdClient()
			opts := append([]Option{withStatsdClient(statsd), WithPoolStats()}, tc.opts...)
			conn, err := NewPool(ctx, postgresDSN, opts...)
			require.NoError(t, err)
			defer conn.Close()

			assert := assert.New(t)
			if err := statsd.Wait(assert, len(wantStats), time.Second); err != nil {
				t.Fatalf("statsd.Wait(): %v", err)
			}
			for _, name := range wantStats {
				calls := statsd.GetCallsByName(name)
				assert.NotEmpty(calls, "expected calls for %s", name)
				for _, call := range calls {
					for _, tag := range tc.wantTags {
						assert.Contains(call.Tags(), tag, "metric %s missing tag %s", name, tag)
					}
				}
			}
		})
	}
}

func withStatsdClient(s instrumentation.StatsdClient) Option {
	return func(c *config) {
		c.statsdClient = s
	}
}
