// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2"
)

type Option = v2.Option

// WithServiceName sets the service name to use for all spans.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithTraceQuery enables tracing query operations.
func WithTraceQuery(enabled bool) Option {
	return v2.WithTraceQuery(enabled)
}

// WithTraceBatch enables tracing batched operations (i.e. pgx.Batch{}).
func WithTraceBatch(enabled bool) Option {
	return v2.WithTraceBatch(enabled)
}

// WithTraceCopyFrom enables tracing pgx.CopyFrom calls.
func WithTraceCopyFrom(enabled bool) Option {
	return v2.WithTraceCopyFrom(enabled)
}

// WithTraceAcquire enables tracing pgxpool connection acquire calls.
func WithTraceAcquire(enabled bool) Option {
	return v2.WithTraceAcquire(enabled)
}

// WithTracePrepare enables tracing prepared statements.
//
//	conn, err := pgx.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.WithTraceConnect())
//	if err != nil {
//		// handle err
//	}
//	defer conn.Close(ctx)
//
//	_, err := conn.Prepare(ctx, "stmt", "select $1::integer")
//	row, err := conn.QueryRow(ctx, "stmt", 1)
func WithTracePrepare(enabled bool) Option {
	return v2.WithTracePrepare(enabled)
}

// WithTraceConnect enables tracing calls to Connect and ConnectConfig.
//
//	pgx.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.WithTraceConnect())
func WithTraceConnect(enabled bool) Option {
	return v2.WithTraceConnect(enabled)
}

// WithPoolStats enables polling of pgxpool.Stat metrics
// ref: https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Stat
// These metrics are submitted to Datadog and are not billed as custom metrics
func WithPoolStats() Option {
	return v2.WithPoolStats()
}
