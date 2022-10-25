// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package pgx provides functions to trace the jackc/pgx package (v5) (https://github.com/jackc/pgx).

package pgx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TracedConn returns a traced *pgx.Conn
func TracedConn(ctx context.Context, connString string, options ...Option) (*pgx.Conn, error) {
	cc, err := pgx.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("contrib/jackc/pgx: invalid connection string [%v]: %w", connString, err)
	}

	cc.Tracer = buildTracer(options...)
	c, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("contrib/jackc/pgx: connect: %w", err)
	}

	return c, nil
}

// TracedConnWithConfig returns a traced *pgx.Conn with the config
func TracedConnWithConfig(ctx context.Context, config *pgx.ConnConfig, options ...Option) (*pgx.Conn, error) {
	config.Tracer = buildTracer(options...)

	return pgx.ConnectConfig(ctx, config)
}

// TracedPool returns a traced *pgxpool.Pool for concurrent use
func TracedPool(ctx context.Context, connString string, options ...Option) (*pgxpool.Pool, error) {
	cc, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("contrib/jackc/pgx: invalid connection string for pool [%v]: %w", connString, err)
	}
	cc.ConnConfig.Tracer = buildTracer(options...)

	p, err := pgxpool.NewWithConfig(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("contrib/jackc/pgx: new pool: %w", err)
	}

	return p, nil
}

// TracedPoolWithConfig returns a traced *pgxpool.Pool with config, for concurrent use
func TracedPoolWithConfig(ctx context.Context, config *pgxpool.Config, options ...Option) (*pgxpool.Pool, error) {
	config.ConnConfig.Tracer = buildTracer(options...)

	return pgxpool.NewWithConfig(ctx, config)
}
