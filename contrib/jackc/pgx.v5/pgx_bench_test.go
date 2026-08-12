// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BenchmarkPgxTracerHooks measures the allocation cost of tagging traced spans with
// connection metadata (net.destination.name/port, db.name, db.user). Not registered
// in .gitlab/benchmarks/micro/gitlab-ci.yml.
func BenchmarkPgxTracerHooks(b *testing.B) {
	ctx := context.Background()

	b.Run("pool_query", func(b *testing.B) {
		opts := append(tracingAllDisabled(), WithTraceQuery(true))
		pool, err := NewPool(ctx, postgresDSN, opts...)
		if err != nil {
			b.Fatal(err)
		}
		defer pool.Close()

		b.ReportAllocs()
		for b.Loop() {
			var x int
			if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&x); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("pool_query_before_connect", func(b *testing.B) {
		cfg, err := pgxpool.ParseConfig(postgresDSN)
		if err != nil {
			b.Fatal(err)
		}
		// A BeforeConnect hook can rewrite host, port, database or user per
		// connection, so it forces every connection-scoped span to read the
		// connection's actual config instead of a cached snapshot.
		cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error { return nil }

		opts := append(tracingAllDisabled(), WithTraceQuery(true))
		pool, err := NewPoolWithConfig(ctx, cfg, opts...)
		if err != nil {
			b.Fatal(err)
		}
		defer pool.Close()

		b.ReportAllocs()
		for b.Loop() {
			var x int
			if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&x); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("conn_query", func(b *testing.B) {
		opts := append(tracingAllDisabled(), WithTraceQuery(true))
		conn, err := Connect(ctx, postgresDSN, opts...)
		if err != nil {
			b.Fatal(err)
		}
		defer conn.Close(ctx)

		b.ReportAllocs()
		for b.Loop() {
			var x int
			if err := conn.QueryRow(ctx, `SELECT 1`).Scan(&x); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("pool_acquire", func(b *testing.B) {
		opts := append(tracingAllDisabled(), WithTraceAcquire(true))
		pool, err := NewPool(ctx, postgresDSN, opts...)
		if err != nil {
			b.Fatal(err)
		}
		defer pool.Close()

		b.ReportAllocs()
		for b.Loop() {
			c, err := pool.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			c.Release()
		}
	})
}
