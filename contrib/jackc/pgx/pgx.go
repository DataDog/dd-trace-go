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
// note that connect trace are not effective this way
func TracedConn(ctx context.Context, connString string, options ...Option) (*pgx.Conn, error) {
	c, err := pgx.Connect(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("contrib/jackc/pgx: connecting: %w", err)
	}

	t := defaults()
	for _, opt := range options {
		opt(t)
	}
	c.Config().Tracer = t

	return c, nil
}

// TracedConnWithConfig returns a traced *pgx.Conn with the config
func TracedConnWithConfig(ctx context.Context, config *pgx.ConnConfig, options ...Option) (*pgx.Conn, error) {
	t := defaults()
	for _, opt := range options {
		opt(t)
	}
	config.Tracer = t

	return pgx.ConnectConfig(ctx, config)
}

// TracedPool returns a traced *pgxpool.Pool for concurrent use
// note that connect traces are not effective this way
func TracedPool(ctx context.Context, connString string, options ...Option) (*pgxpool.Pool, error) {
	p, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("contrib/jackc/pgx: new pool: %w", err)
	}

	t := defaults()
	for _, opt := range options {
		opt(t)
	}
	p.Config().ConnConfig.Tracer = t

	return p, nil
}

// TracedPoolWithConfig returns a traced *pgxpool.Pool with config, for concurrent use
func TracedPoolWithConfig(ctx context.Context, config *pgxpool.Config, options ...Option) (*pgxpool.Pool, error) {
	t := defaults()
	for _, opt := range options {
		opt(t)
	}
	config.ConnConfig.Tracer = t

	return pgxpool.NewWithConfig(ctx, config)
}

