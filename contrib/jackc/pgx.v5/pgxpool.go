// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, connString string, opts ...Option) (*pgxpool.Pool, error) {
	return v2.NewPool(ctx, connString, opts...)
}

func NewPoolWithConfig(ctx context.Context, config *pgxpool.Config, opts ...Option) (*pgxpool.Pool, error) {
	return v2.NewPoolWithConfig(ctx, config, opts...)
}
