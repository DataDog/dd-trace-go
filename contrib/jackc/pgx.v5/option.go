// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import "gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

type config struct {
	serviceName   string
	traceQuery    bool
	traceBatch    bool
	traceCopyFrom bool
	tracePrepare  bool
	traceConnect  bool
}

func defaultConfig() *config {
	return &config{
		serviceName:   namingschema.ServiceName(defaultServiceName),
		traceQuery:    true,
		traceBatch:    true,
		traceCopyFrom: true,
		tracePrepare:  true,
		traceConnect:  true,
	}
}

type Option func(*config)

// WithServiceName sets the service name to use for all spans.
func WithServiceName(name string) Option {
	return func(c *config) {
		c.serviceName = name
	}
}

// WithTraceQuery enables tracing query operations.
func WithTraceQuery(enabled bool) Option {
	return func(c *config) {
		c.traceQuery = enabled
	}
}

// WithTraceBatch enables tracing batched operations (i.e. pgx.Batch{}).
func WithTraceBatch(enabled bool) Option {
	return func(c *config) {
		c.traceBatch = enabled
	}
}

// WithTraceCopyFrom enables tracing pgx.CopyFrom calls.
func WithTraceCopyFrom(enabled bool) Option {
	return func(c *config) {
		c.traceCopyFrom = enabled
	}
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
	return func(c *config) {
		c.tracePrepare = enabled
	}
}

// WithTraceConnect enables tracing calls to Connect and ConnectConfig.
//
//	pgx.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.WithTraceConnect())
func WithTraceConnect(enabled bool) Option {
	return func(c *config) {
		c.traceConnect = enabled
	}
}
