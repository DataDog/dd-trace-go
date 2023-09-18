// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgxpool

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/jackc/pgx.v5"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func init() {
	tracer.MarkIntegrationImported("github.com/jackc/pgx/v5/pgxpool")
}

func New(ctx context.Context, connString string, opts ...pgx_v5.Option) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	return NewWithConfig(ctx, config, opts...)
}

func NewWithConfig(ctx context.Context, config *pgxpool.Config, opts ...pgx_v5.Option) (*pgxpool.Pool, error) {
	config.ConnConfig.Tracer = pgx_v5.New(opts...)

	return pgxpool.NewWithConfig(ctx, config)
}
