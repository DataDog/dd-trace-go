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
	"os"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var _ driver.Conn = (*tracedConn)(nil)

type queryType string

const (
	queryTypeConnect  queryType = "Connect"
	queryTypeQuery              = "Query"
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
		span := tc.tryStartTrace(ctx, queryTypeBegin, "", start, &tracer.SQLCommentCarrier{}, err)
		if span != nil {
			defer func() {
				span.Finish(tracer.WithError(err))
			}()
		}
		if err != nil {
			return nil, err
		}
		return &tracedTx{tx, tc.traceParams, ctx}, nil
	}
	tx, err = tc.Conn.Begin()
	span := tc.tryStartTrace(ctx, queryTypeBegin, "", start, &tracer.SQLCommentCarrier{}, err)
	if span != nil {
		defer func() {
			span.Finish(tracer.WithError(err))
		}()
	}
	if err != nil {
		return nil, err
	}
	return &tracedTx{tx, tc.traceParams, ctx}, nil
}

func (tc *tracedConn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	start := time.Now()
	if connPrepareCtx, ok := tc.Conn.(driver.ConnPrepareContext); ok {
		sqlCommentCarrier := tracer.SQLCommentCarrier{}
		span := tc.tryStartTrace(ctx, queryTypePrepare, query, start, &sqlCommentCarrier, err)
		if span != nil {
			go func() {
				span.Finish(tracer.WithError(err))
			}()
		}
		stmt, err := connPrepareCtx.PrepareContext(ctx, sqlCommentCarrier.CommentedQuery(query))
		if err != nil {
			return nil, err
		}

		return &tracedStmt{Stmt: stmt, traceParams: tc.traceParams, ctx: ctx, query: query}, nil
	}
	sqlCommentCarrier := tracer.SQLCommentCarrier{}
	span := tc.tryStartTrace(ctx, queryTypePrepare, query, start, &sqlCommentCarrier, err)
	if span != nil {
		go func() {
			span.Finish(tracer.WithError(err))
		}()
	}
	stmt, err = tc.Prepare(sqlCommentCarrier.CommentedQuery(query))
	if err != nil {
		return nil, err
	}

	return &tracedStmt{Stmt: stmt, traceParams: tc.traceParams, ctx: ctx, query: query}, nil
}

//func (tc *tracedConn) commentedQuery(query string, spanCtx ddtrace.SpanContext) string {
//	return comment.OnQuery(query, map[string]string{ext.ServiceName: tc.cfg.serviceName, "dd.span_id": strconv.FormatUint(spanCtx.SpanID(), 10), "dd.trace_id": strconv.FormatUint(spanCtx.TraceID(), 10)}, tc.meta)
//}

func (tc *tracedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, err error) {
	start := time.Now()
	if execContext, ok := tc.Conn.(driver.ExecerContext); ok {
		sqlCommentCarrier := tracer.SQLCommentCarrier{}
		span := tc.tryStartTrace(ctx, queryTypeBegin, query, start, &sqlCommentCarrier, err)
		if span != nil {
			defer func() {
				span.Finish(tracer.WithError(err))
			}()
		}
		r, err := execContext.ExecContext(ctx, sqlCommentCarrier.CommentedQuery(query), args)
		return r, err
	}
	if execer, ok := tc.Conn.(driver.Execer); ok {
		dargs, err := namedValueToValue(args)
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		sqlCommentCarrier := tracer.SQLCommentCarrier{}
		span := tc.tryStartTrace(ctx, queryTypeExec, query, start, &sqlCommentCarrier, err)
		if span != nil {
			defer func() {
				span.Finish(tracer.WithError(err))
			}()
		}
		r, err = execer.Exec(sqlCommentCarrier.CommentedQuery(query), dargs)
		return r, err
	}
	return nil, driver.ErrSkip
}

// tracedConn has a Ping method in order to implement the pinger interface
func (tc *tracedConn) Ping(ctx context.Context) (err error) {
	start := time.Now()
	if pinger, ok := tc.Conn.(driver.Pinger); ok {
		err = pinger.Ping(ctx)
	}
	span := tc.tryStartTrace(ctx, queryTypePing, "", start, &tracer.SQLCommentCarrier{}, err)
	if span != nil {
		go func() {
			span.Finish(tracer.WithError(err))
		}()
	}
	return err
}

func (tc *tracedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	start := time.Now()
	if queryerContext, ok := tc.Conn.(driver.QueryerContext); ok {
		sqlCommentCarrier := tracer.SQLCommentCarrier{}
		span := tc.tryStartTrace(ctx, queryTypeQuery, query, start, &sqlCommentCarrier, err)
		if span != nil {
			go func() {
				span.Finish(tracer.WithError(err))
			}()
		}
		rows, err := queryerContext.QueryContext(ctx, sqlCommentCarrier.CommentedQuery(query), args)

		return rows, err
	}
	if queryer, ok := tc.Conn.(driver.Queryer); ok {
		dargs, err := namedValueToValue(args)
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		sqlCommentCarrier := tracer.SQLCommentCarrier{}
		span := tc.tryStartTrace(ctx, queryTypeQuery, query, start, &sqlCommentCarrier, err)
		if span != nil {
			go func() {
				span.Finish(tracer.WithError(err))
			}()
		}
		rows, err = queryer.Query(sqlCommentCarrier.CommentedQuery(query), dargs)
		return rows, err
	}
	return nil, driver.ErrSkip
}

func (tc *tracedConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := tc.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

var _ driver.SessionResetter = (*tracedConn)(nil)

// ResetSession implements driver.SessionResetter
func (tc *tracedConn) ResetSession(ctx context.Context) error {
	if resetter, ok := tc.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	// If driver doesn't implement driver.SessionResetter there's nothing to do
	return nil
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

// tryStartTrace will create a span using the given arguments, but will act as a no-op when err is driver.ErrSkip.
func (tp *traceParams) tryStartTrace(ctx context.Context, qtype queryType, query string, startTime time.Time, sqlCommentCarrier *tracer.SQLCommentCarrier, err error) (span tracer.Span) {
	if err == driver.ErrSkip {
		// Not a user error: driver is telling sql package that an
		// optional interface method is not implemented. There is
		// nothing to trace here.
		// See: https://github.com/DataDog/dd-trace-go/issues/270
		return
	}
	if _, exists := tracer.SpanFromContext(ctx); tp.cfg.childSpansOnly && !exists {
		return nil
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
	span, _ = tracer.StartSpanFromContext(ctx, name, opts...)
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

	err = tracer.Inject(span.Context(), sqlCommentCarrier)
	if err != nil {
		// this should never happen
		fmt.Fprintf(os.Stderr, "contrib/database/sql: failed to inject query comments: %v\n", err)
	}
	// TODO: Figure out if there's a better way to add those additional tags
	sqlCommentCarrier.Set(ext.ServiceName, tp.cfg.serviceName)
	for k, v := range tp.meta {
		sqlCommentCarrier.Set(k, v)
	}
	
	return span
}
