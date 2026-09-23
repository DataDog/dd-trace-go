// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package pgx instruments pgx connections and pools with tracing and SQL injection
// monitoring. With AppSec and RASP enabled, SQL passed to Query, QueryRow, Exec,
// and SendBatch is evaluated in the incoming request's security context, even when
// query or batch tracing is disabled. Native pgx monitoring reports attacks but
// cannot block SQL execution; its tracing hooks cannot return an error.
//
// Queries already checked by Datadog's database/sql integration are not evaluated
// again, and direct ExecContext/QueryContext calls retain their blocking behavior.
// Prepared statements through database/sql do not run that blocking check: when
// the pgx hooks are installed, they are monitored but cannot be blocked.
// Monitoring requires the instrumented incoming request context. It also evaluates
// transaction control statements sent by pgx through Exec: Begin plus Commit or
// Rollback adds two evaluations, even with no user SQL statements.
// The hooks see SQL before pgx query rewriting and prepared-statement name lookup;
// SQL produced by custom QueryRewriters and execution by statement name are not
// fully covered. Bound parameter values are not interpolated into the SQL.
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
	connConfig.Tracer = wrapPgxTracer(connConfig, opts...)
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
