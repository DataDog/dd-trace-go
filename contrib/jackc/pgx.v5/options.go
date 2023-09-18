// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx_v5

type Option func(*trace)

// WithServiceName sets the service name to use for all spans.
func WithServiceName(name string) Option {
	return func(t *trace) {
		t.serviceName = name
	}
}

// WithTraceBatch enables tracing batched operations (i.e. pgx.Batch{}).
func WithTraceBatch() Option {
	return func(t *trace) {
		t.traceBatch = true
	}
}

// WithTraceCopyFrom enables tracing pgx.v5.CopyFrom calls.
func WithTraceCopyFrom() Option {
	return func(t *trace) {
		t.traceCopyFrom = true
	}
}

// WithTracePrepare enables tracing prepared statements.
//
//	conn, err := pgx.v5.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.v5.WithTraceConnect())
//	if err != nil {
//		// handle err
//	}
//	defer conn.Close(ctx)
//
//	_, err := conn.Prepare(ctx, "stmt", "select $1::integer")
//	row, err := conn.QueryRow(ctx, "stmt", 1)
func WithTracePrepare() Option {
	return func(t *trace) {
		t.tracePrepare = true
	}
}

// WithTraceConnect enables tracing calls to Connect and ConnectConfig.
//
//	pgx.v5.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.v5.WithTraceConnect())
func WithTraceConnect() Option {
	return func(t *trace) {
		t.traceConnect = true
	}
}
