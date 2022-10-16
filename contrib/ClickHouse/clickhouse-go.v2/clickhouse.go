// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package clickhouse provides functions to trace the ClickHouse/clickhouse-go package (https://github.com/ClickHouse/clickhouse-go).
//
// `WrapConnection` will wrap a ClickHouse `clickhouse.Connection` and return a new struct with all
// the same methods, so should be seamless for existing applications. It also
// has an additional `WithContext` method which can be used to connect a span
// to an existing trace.
package clickhouse // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/clickhouse/clickhouse-go.v2"

import (
	"context"
	"encoding/binary"
	"errors"
	"math"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// WrapConnection wraps a native clickhouse.Connection so that all requests are traced using the
// default tracer with the service name "clickhouse".
// If you need a tracer for clickhouse.OpenDB(&clickhouse.Options) database/sql interface
// take a look at "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
func WrapConnection(conn clickhouse.Conn, opts ...Option) *Connection {
	cfg := new(connectionConfig)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	log.Debug("contrib/clickhouse/clickhouse-go: Wrapping Connection: %#v", cfg)
	return &Connection{
		Conn: conn,
		cfg:  cfg,
		ctx:  context.Background(),
	}
}

// A Connection is used to trace requests to the ClickHouse server.
type Connection struct {
	driver.Conn
	cfg *connectionConfig
	ctx context.Context
}

type Batch struct {
	driver.Batch
	cfg *connectionConfig
	ctx context.Context
}

func WrapBatch(ctx context.Context, batch driver.Batch) *Batch {
	cfg := new(connectionConfig)
	defaults(cfg)
	log.Debug("contrib/clickhouse/clickhouse-go: Wrapping Batch interface: %#v", cfg)
	tb := &Batch{batch, cfg, ctx}
	return tb
}

// Append invokes Batch.Append.
func (b *Batch) Append(v ...interface{}) error {
	return b.Batch.Append(v...)
}

// AppendStruct invokes Batch.AppendStruct.
func (b *Batch) AppendStruct(v interface{}) error {
	return b.Batch.AppendStruct(v)
}

// Column invokes Batch.Column.
func (b *Batch) Column(idx int) driver.BatchColumn {
	return b.Batch.Column(idx)
}

// IsSent invokes Batch.IsSent.
func (b *Batch) IsSent() bool {
	return b.Batch.IsSent()
}

// WithContext adds the specified context to the traced Connection structure.
func (c *Connection) WithContext(ctx context.Context) *Connection {
	return &Connection{
		Conn: c.Conn,
		cfg:  c.cfg,
		ctx:  clickhouse.Context(ctx),
	}
}

// WithOLTPSpan adds functionality to pass Span context with DataDog identifiers
// credits to https://github.com/boostchicken/opentelemetry-collector-contrib/blob/datadog-receiver/receiver/datadogreceiver/translator.go
func withOLTPSpanContext(span ddtrace.Span) trace.SpanContext {
	currentSpanID := span.Context().SpanID()
	currentTraceID := span.Context().TraceID()

	otelTraceID := uInt64ToTraceID(0, currentSpanID)
	otelSpanID := uInt64ToSpanID(currentTraceID)

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: otelTraceID,
		SpanID:  otelSpanID,
	})
	return spanContext
}

func uInt64ToTraceID(high, low uint64) trace.TraceID {
	traceID := [16]byte{}
	binary.BigEndian.PutUint64(traceID[:8], high)
	binary.BigEndian.PutUint64(traceID[8:], low)
	return traceID
}

func uInt64ToSpanID(id uint64) trace.SpanID {
	spanID := [8]byte{}
	binary.BigEndian.PutUint64(spanID[:], id)
	return spanID
}

// startSpan starts a span for a Connection from the context passed to the function.
func (c *Connection) startSpan(query string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeClickHouse),
		tracer.ServiceName(c.cfg.serviceName),
	}
	if c.cfg.resourceName != "" {
		opts = append(opts, tracer.Tag(ext.ResourceName, c.cfg.resourceName))
	} else {
		opts = append(opts, tracer.Tag(ext.ResourceName, query))
	}

	if !math.IsNaN(c.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, c.cfg.analyticsRate))
	}
	if c.cfg.withStats {
		stats := c.Conn.Stats()
		opts = append(opts, tracer.Tag(ext.ClickHouseConnectionOpen, stats.Open))
		opts = append(opts, tracer.Tag(ext.ClickHouseConnectionIdle, stats.Idle))
		opts = append(opts, tracer.Tag(ext.ClickHouseMaxOpenConnections, stats.MaxOpenConns))
		opts = append(opts, tracer.Tag(ext.ClickHouseMaxIdleConnections, stats.MaxIdleConns))
	}
	span, _ := tracer.StartSpanFromContext(c.ctx, ext.ClickHouseQuery, opts...)
	return span
}

// startSpan starts a span for Batch from the context passed to the function.
func (b *Batch) startSpan(resourceName string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeClickHouse),
		tracer.ServiceName(b.cfg.serviceName),
		tracer.Tag(ext.DBType, "clickhouse"),
	}
	if b.cfg.resourceName != "" {
		opts = append(opts, tracer.Tag(ext.ResourceName, b.cfg.resourceName))
	} else {
		opts = append(opts, tracer.Tag(ext.ResourceName, resourceName))
	}

	if !math.IsNaN(b.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, b.cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(b.ctx, ext.ClickHouseBatch, opts...)
	return span
}

// wrapped methods:

// Query invokes and traces Connection.Query.
func (c *Connection) Query(query string, args ...interface{}) (rows driver.Rows, err error) {
	span := c.startSpan(query)
	args = oltpSpan(c, span, args)
	rows, err = c.Conn.Query(c.ctx, query, args)
	span.Finish(tracer.WithError(err))
	return rows, err
}

// Select invokes and traces Connection.Select.
func (c *Connection) Select(dest interface{}, query string, args ...interface{}) error {
	span := c.startSpan(query)
	args = oltpSpan(c, span, args)
	err := c.Conn.Select(c.ctx, dest, query, args)
	span.Finish(tracer.WithError(err))
	return err

}

func oltpSpan(c *Connection, span ddtrace.Span, args []interface{}) []interface{} {
	if c.cfg.withOltpSpan {
		opt := withOLTPSpanContext(span)
		withSpan := clickhouse.WithSpan(opt)
		args = append(args, withSpan)
		return args
	}
	return args
}

// QueryRow invokes and traces Connection.QueryRow.
func (c *Connection) QueryRow(query string, args ...interface{}) (row driver.Row) {
	span := c.startSpan(query)
	args = oltpSpan(c, span, args)
	row = c.Conn.QueryRow(c.ctx, query, args)
	span.Finish()
	return row
}

// Exec invokes and traces Connection.Exec.
func (c *Connection) Exec(query string, args ...interface{}) error {
	span := c.startSpan(query)
	args = oltpSpan(c, span, args)
	err := c.Conn.Exec(c.ctx, query, args)
	span.Finish(tracer.WithError(err))
	return err
}

// PrepareBatch invokes and traces Connection.PrepareBatch.
func (c *Connection) PrepareBatch(query string) (driver.Batch, error) {
	span := c.startSpan(query)
	batch, err := c.Conn.PrepareBatch(c.ctx, query)
	span.Finish(tracer.WithError(err))
	return WrapBatch(c.ctx, batch), err
}

// AsyncInsert invokes and traces Connection.AsyncInsert.
func (c *Connection) AsyncInsert(query string, wait bool) error {
	span := c.startSpan(query)
	err := c.Conn.AsyncInsert(c.ctx, query, wait)
	span.Finish(tracer.WithError(err))
	return err
}

// Close closes the Connection and finish the span created on WrapConnection call.
func (c *Connection) Close() error {
	span := c.startSpan("Close")
	err := c.Conn.Close()
	span.Finish(tracer.WithError(err))
	return err
}

// Send invokes and traces Batch.Send.
func (b *Batch) Send() error {
	span := b.startSpan("BatchSend")
	if b.Batch == nil {
		span.Finish(tracer.WithError(errors.New("invalid batch, Send() not executed")))
		return nil
	}
	err := b.Batch.Send()
	span.Finish(tracer.WithError(err))
	return err

}

// Flush invokes and traces Batch.Flush.
func (b *Batch) Flush() error {
	span := b.startSpan("BatchFlush")
	if b.Batch == nil {
		span.Finish(tracer.WithError(errors.New("invalid batch, Send() not executed")))
		return nil
	}
	err := b.Batch.Flush()
	span.Finish(tracer.WithError(err))
	return err
}

// Abort invokes and traces Batch.Abort.
func (b *Batch) Abort() error {
	span := b.startSpan("BatchFlush")
	err := b.Batch.Abort()
	span.Finish(tracer.WithError(err))
	return err
}
