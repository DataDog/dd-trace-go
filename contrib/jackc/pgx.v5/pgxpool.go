// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, connString string, opts ...Option) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	return NewPoolWithConfig(ctx, config, opts...)
}

func NewPoolWithConfig(ctx context.Context, config *pgxpool.Config, opts ...Option) (*pgxpool.Pool, error) {
	config.ConnConfig.Tracer = newPgxTracer(opts...)
	return pgxpool.NewWithConfig(ctx, config)
}
