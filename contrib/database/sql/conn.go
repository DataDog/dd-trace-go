// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"

import (
	"context"
	"database/sql/driver"
	"fmt"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var _ driver.Conn = (*tracedConn)(nil)

type queryType string

const (
	queryTypeQuery    queryType = "Query"
	queryTypePing               = "Ping"
	queryTypePrepare            = "Prepare"
	queryTypeExec               = "Exec"
	queryTypeBegin              = "Begin"
	queryTypeClose              = "Close"
	queryTypeCommit             = "Commit"
	queryTypeRollback           = "Rollback"
)

type tracedConn struct {
	driver.Conn
	*traceParams
}

func (tc *tracedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) {
	start := time.Now()
	if connBeginTx, ok := tc.Conn.(driver.ConnBeginTx); ok {
		tx, err = connBeginTx.BeginTx(ctx, opts)
		tc.tryTrace(ctx, queryTypeBegin, "", start, err)
		if err != nil {
			return nil, err
		}
		return &tracedTx{tx, tc.traceParams, ctx}, nil
	}
	tx, err = tc.Conn.Begin()
	tc.tryTrace(ctx, queryTypeBegin, "", start, err)
	if err != nil {
		return nil, err
	}
	return &tracedTx{tx, tc.traceParams, ctx}, nil
}

func (tc *tracedConn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	start := time.Now()
	if connPrepareCtx, ok := tc.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := connPrepareCtx.PrepareContext(ctx, query)
		tc.tryTrace(ctx, queryTypePrepare, query, start, err)
		if err != nil {
			return nil, err
		}
		return &tracedStmt{stmt, tc.traceParams, ctx, query}, nil
	}
	stmt, err = tc.Prepare(query)
	tc.tryTrace(ctx, queryTypePrepare, query, start, err)
	if err != nil {
		return nil, err
	}
	return &tracedStmt{stmt, tc.traceParams, ctx, query}, nil
}

func (tc *tracedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if execer, ok := tc.Conn.(driver.Execer); ok {
		return execer.Exec(query, args)
	}
	return nil, driver.ErrSkip
}

func (tc *tracedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, err error) {
	start := time.Now()
	if execContext, ok := tc.Conn.(driver.ExecerContext); ok {
		r, err := execContext.ExecContext(ctx, query, args)
		tc.tryTrace(ctx, queryTypeExec, query, start, err)
		return r, err
	}
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	r, err = tc.Exec(query, dargs)
	tc.tryTrace(ctx, queryTypeExec, query, start, err)
	return r, err
}

// tracedConn has a Ping method in order to implement the pinger interface
func (tc *tracedConn) Ping(ctx context.Context) (err error) {
	start := time.Now()
	if pinger, ok := tc.Conn.(driver.Pinger); ok {
		err = pinger.Ping(ctx)
	}
	tc.tryTrace(ctx, queryTypePing, "", start, err)
	return err
}

func (tc *tracedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if queryer, ok := tc.Conn.(driver.Queryer); ok {
		return queryer.Query(query, args)
	}
	return nil, driver.ErrSkip
}

func (tc *tracedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	start := time.Now()
	if queryerContext, ok := tc.Conn.(driver.QueryerContext); ok {
		rows, err := queryerContext.QueryContext(ctx, query, args)
		tc.tryTrace(ctx, queryTypeQuery, query, start, err)
		return rows, err
	}
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	rows, err = tc.Query(query, dargs)
	tc.tryTrace(ctx, queryTypeQuery, query, start, err)
	return rows, err
}

func (tc *tracedConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := tc.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

// traceParams stores all information related to tracing the driver.Conn
type traceParams struct {
	cfg        *config
	driverName string
	meta       map[string]string
}

type contextKey int

const spanTagsKey contextKey = 0 // map[string]string

// WithSpanTags creates a new context containing the given set of tags. They will be added
// to any query created with the returned context.
func WithSpanTags(ctx context.Context, tags map[string]string) context.Context {
	return context.WithValue(ctx, spanTagsKey, tags)
}

// tryTrace will create a span using the given arguments, but will act as a no-op when err is driver.ErrSkip.
func (tp *traceParams) tryTrace(ctx context.Context, qtype queryType, query string, startTime time.Time, err error) {
	if err == driver.ErrSkip {
		// Not a user error: driver is telling sql package that an
		// optional interface method is not implemented. There is
		// nothing to trace here.
		// See: https://github.com/DataDog/dd-trace-go/issues/270
		return
	}
	name := fmt.Sprintf("%s.query", tp.driverName)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(tp.cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.StartTime(startTime),
	}
	if !math.IsNaN(tp.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tp.cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(ctx, name, opts...)
	resource := string(qtype)
	if query != "" {
		resource = query
	}
	span.SetTag("sql.query_type", string(qtype))
	span.SetTag(ext.ResourceName, resource)
	for k, v := range tp.meta {
		span.SetTag(k, v)
	}
	if meta, ok := ctx.Value(spanTagsKey).(map[string]string); ok {
		for k, v := range meta {
			span.SetTag(k, v)
		}
	}
	span.Finish(tracer.WithError(err))
}
