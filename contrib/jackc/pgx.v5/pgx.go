// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/jackc/pgx/v5"
)

const (
	defaultServiceName = "postgres.db"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageJackcPGXV5)
}

type Batch = pgx.Batch

func Connect(ctx context.Context, connString string, opts ...Option) (*pgx.Conn, error) {
	connConfig, err := pgx.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	return ConnectConfig(ctx, connConfig, opts...)
}

func ConnectConfig(ctx context.Context, connConfig *pgx.ConnConfig, opts ...Option) (*pgx.Conn, error) {
	// The tracer must be set in the config before calling connect
	// as pgx takes ownership of the config. QueryTracer traces
	// may work, but none of the others will, as they're set in
	// unexported fields in the config in the pgx.connect function.
	connConfig.Tracer = newPgxTracer(opts...)
	return pgx.ConnectConfig(ctx, connConfig)
}
