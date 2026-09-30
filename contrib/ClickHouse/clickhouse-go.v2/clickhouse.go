// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package clickhouse

import (
	"context"
	"io"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func init() {
	instrumentation.Load(instrumentation.PackageClickHouseV2)
}

const (
	// ComponentName is the component tag on spans created by this package.
	ComponentName = "ClickHouse/clickhouse-go.v2"

	// DBSystemName is the db.system tag on spans created by this package.
	DBSystemName = "clickhouse"

	// OperationName is the Datadog operation name for traced ClickHouse calls.
	OperationName = "clickhouse.query"
)

// Wrap traces calls on a ClickHouse connection:
//
//	conn = clickhousetrace.Wrap(conn, clickhousetrace.WithPeerService("analytics"))
//	err := conn.Ping(ctx)
//
// Use the wrapped connection in place of conn, and pass request contexts to
// its methods to attach spans to a parent. Options set span metadata without
// changing the underlying connection. Spans inherit the application's service
// name unless [WithService] is set. Wrap(nil) returns nil.
func Wrap(conn clickhouse.Conn, opts ...Option) clickhouse.Conn {
	if conn == nil {
		return nil
	}
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	spanOpts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.Tag(ext.Component, ComponentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, DBSystemName),
	}
	if cfg.serviceName != "" {
		spanOpts = append(spanOpts, instrumentation.ServiceNameWithSource(
			cfg.serviceName, instrumentation.ServiceSourceWithServiceOption,
		))
	}
	for key, value := range map[string]string{
		ext.PeerService: cfg.peerService,
		ext.TargetHost:  cfg.host,
		ext.TargetPort:  cfg.port,
		ext.DBName:      cfg.database,
		ext.DBUser:      cfg.user,
	} {
		if value != "" {
			spanOpts = append(spanOpts, tracer.Tag(key, value))
		}
	}
	cfg.spanConfig = tracer.NewStartSpanConfig(spanOpts...)
	return &tracedConn{Conn: conn, cfg: cfg}
}

// tracedConn delegates to a ClickHouse connection and traces selected calls.
type tracedConn struct {
	clickhouse.Conn
	cfg *config
}

// trace records a completed driver call with the supplied context and metadata.
func trace(ctx context.Context, cfg *config, queryType, query string, startTime time.Time, err error) {
	resource := query
	if cfg.resourceName != "" {
		resource = cfg.resourceName
	} else if resource == "" {
		resource = queryType
	}

	span, _ := tracer.StartSpanFromContext(ctx, OperationName,
		tracer.WithTags(map[string]any{
			ext.ResourceName: resource,
			"sql.query_type": queryType,
		}),
		tracer.WithStartSpanConfig(cfg.spanConfig),
		tracer.StartTime(startTime),
	)
	if err != nil && cfg.errCheck != nil && !cfg.errCheck(err) {
		err = nil
	}
	span.Finish(tracer.WithError(err))
}

// Select traces the full Select call, including its decoding into dest.
func (c *tracedConn) Select(ctx context.Context, dest any, query string, args ...any) error {
	start := time.Now()
	err := c.Conn.Select(ctx, dest, query, args...)
	trace(ctx, c.cfg, "Query", query, start, err)
	return err
}

// Query traces the call that opens rows; later iteration and scans are untraced.
func (c *tracedConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	start := time.Now()
	rows, err := c.Conn.Query(ctx, query, args...)
	trace(ctx, c.cfg, "Query", query, start, err)
	return rows, err
}

// QueryRow reports an error already available from Row.Err when the call returns.
// A later Scan call is outside the span.
func (c *tracedConn) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	start := time.Now()
	row := c.Conn.QueryRow(ctx, query, args...)
	trace(ctx, c.cfg, "Query", query, start, row.Err())
	return row
}

// PrepareBatch traces preparation and returns a batch whose Send, Flush, and
// Abort calls are traced separately. The batch uses ctx for those later spans.
func (c *tracedConn) PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	start := time.Now()
	batch, err := c.Conn.PrepareBatch(ctx, query, opts...)
	trace(ctx, c.cfg, "Prepare", query, start, err)
	if err != nil {
		return nil, err
	}
	return &tracedBatch{Batch: batch, cfg: c.cfg, query: query, ctx: ctx}, nil
}

// Exec traces the driver call and returns its error unchanged.
func (c *tracedConn) Exec(ctx context.Context, query string, args ...any) error {
	start := time.Now()
	err := c.Conn.Exec(ctx, query, args...)
	trace(ctx, c.cfg, "Exec", query, start, err)
	return err
}

// AsyncInsert traces the driver call as an Exec operation. The wait argument is
// passed through unchanged; with wait false, the span does not await insertion.
func (c *tracedConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	start := time.Now()
	err := c.Conn.AsyncInsert(ctx, query, wait, args...)
	trace(ctx, c.cfg, "Exec", query, start, err)
	return err
}

// QueryFormat traces the call that opens the reader; later reads are untraced.
func (c *tracedConn) QueryFormat(ctx context.Context, format, query string, args ...any) (io.ReadCloser, error) {
	start := time.Now()
	rc, err := c.Conn.QueryFormat(ctx, format, query, args...)
	trace(ctx, c.cfg, "Query", query, start, err)
	return rc, err
}

// InsertFormat traces the driver call, including its reads from data.
func (c *tracedConn) InsertFormat(ctx context.Context, format, query string, data io.Reader) error {
	start := time.Now()
	err := c.Conn.InsertFormat(ctx, format, query, data)
	trace(ctx, c.cfg, "Exec", query, start, err)
	return err
}

// Ping traces a connectivity check with "Ping" as its default resource.
func (c *tracedConn) Ping(ctx context.Context) error {
	start := time.Now()
	err := c.Conn.Ping(ctx)
	trace(ctx, c.cfg, "Ping", "", start, err)
	return err
}
