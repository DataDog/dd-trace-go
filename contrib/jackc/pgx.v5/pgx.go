// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2"

	"github.com/jackc/pgx/v5"
)

type Batch = pgx.Batch

// Connect is equivalent to pgx.Connect providing a connection augmented with tracing.
func Connect(ctx context.Context, connString string, opts ...Option) (*pgx.Conn, error) {
	return v2.Connect(ctx, connString, opts...)
}

// ConnectConfig is equivalent to pgx.ConnectConfig providing a connection augmented with tracing.
func ConnectConfig(ctx context.Context, connConfig *pgx.ConnConfig, opts ...Option) (*pgx.Conn, error) {
	return v2.ConnectConfig(ctx, connConfig, opts...)
}

// ConnectWithOptions is equivalent to pgx.ConnectWithOptions providing a connection augmented with tracing.
func ConnectWithOptions(ctx context.Context, connString string, options pgx.ParseConfigOptions, tracerOpts ...Option) (*pgx.Conn, error) {
	return v2.ConnectWithOptions(ctx, connString, options, tracerOpts...)
}
