// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/jackc/pgx/v5"
)

const (
	componentName      = "jackc/pgx.v5"
	defaultServiceName = "postgres.db"
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/jackc/pgx.v5")
}

// Deprecated: this type is unused internally so it will be removed in a future release, please use pgx.Batch instead.
type Batch = pgx.Batch

// Connect is equivalent to pgx.Connect providing a connection augmented with tracing.
func Connect(ctx context.Context, connString string, opts ...Option) (*pgx.Conn, error) {
	connConfig, err := pgx.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	return ConnectConfig(ctx, connConfig, opts...)
}

// ConnectConfig is equivalent to pgx.ConnectConfig providing a connection augmented with tracing.
func ConnectConfig(ctx context.Context, connConfig *pgx.ConnConfig, opts ...Option) (*pgx.Conn, error) {
	// The tracer must be set in the config before calling connect
	// as pgx takes ownership of the config. QueryTracer traces
	// may work, but none of the others will, as they're set in
	// unexported fields in the config in the pgx.connect function.
	connConfig.Tracer = wrapPgxTracer(connConfig.Tracer, opts...)
	return pgx.ConnectConfig(ctx, connConfig)
}

// ConnectWithOptions is equivalent to pgx.ConnectWithOptions providing a connection augmented with tracing.
func ConnectWithOptions(ctx context.Context, connString string, options pgx.ParseConfigOptions, tracerOpts ...Option) (*pgx.Conn, error) {
	connConfig, err := pgx.ParseConfigWithOptions(connString, options)
	if err != nil {
		return nil, err
	}
	return ConnectConfig(ctx, connConfig, tracerOpts...)
}
