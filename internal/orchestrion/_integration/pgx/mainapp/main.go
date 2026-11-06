// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the pgx constructors from package main. TestMainApp
// builds and runs it.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Nothing listens on this port. A failed connect still reports a pgx.connect
// span, so the app needs no database.
const dsn = "postgres://user:pass@127.0.0.1:1/db?sslmode=disable&connect_timeout=2"

func main() {
	ctx := context.Background()

	if _, err := pgx.Connect(ctx, dsn); err == nil {
		panic("pgx.Connect: expected an error")
	}
	connectAliased(ctx)

	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		panic(err)
	}
	if _, err := pgx.ConnectConfig(ctx, connCfg); err == nil {
		panic("pgx.ConnectConfig: expected an error")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	ping(ctx, pool)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic(err)
	}
	pool, err = pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		panic(err)
	}
	ping(ctx, pool)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

// ping acquires a connection, which makes the pool try to open one.
func ping(ctx context.Context, pool *pgxpool.Pool) {
	defer pool.Close()
	if err := pool.Ping(ctx); err == nil {
		panic("pgxpool.Pool.Ping: expected an error")
	}
}
