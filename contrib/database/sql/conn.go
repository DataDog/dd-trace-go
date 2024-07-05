// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"

import (
	"context"
	"database/sql/driver"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var _ driver.Conn = (*TracedConn)(nil)

// QueryType represents the different available traced db queries.
type QueryType string

const (
	// QueryTypeConnect is used for Connect traces.
	QueryTypeConnect QueryType = "Connect"
	// QueryTypeQuery is used for Query traces.
	QueryTypeQuery = "Query"
	// QueryTypePing is used for Ping traces.
	QueryTypePing = "Ping"
	// QueryTypePrepare is used for Prepare traces.
	QueryTypePrepare = "Prepare"
	// QueryTypeExec is used for Exec traces.
	QueryTypeExec = "Exec"
	// QueryTypeBegin is used for Begin traces.
	QueryTypeBegin = "Begin"
	// QueryTypeClose is used for Close traces.
	QueryTypeClose = "Close"
	// QueryTypeCommit is used for Commit traces.
	QueryTypeCommit = "Commit"
	// QueryTypeRollback is used for Rollback traces.
	QueryTypeRollback = "Rollback"
)

const (
	keyDBMTraceInjected = "_dd.dbm_trace_injected"
)

// TracedConn holds a traced connection with tracing parameters.
type TracedConn struct {
	driver.Conn
	*traceParams
}

// checkQuerySafety runs ASM RASP SQLi checks on the query to verify if it can safely be run.
// If it's unsafe to run, an *events.BlockingSecurityEvent is returned
func checkQuerySecurity(ctx context.Context, query, driver string) error {
	if !appsec.Enabled() {
		return nil
	}
	return sqlsec.ProtectSQLOperation(ctx, query, driver)
}

// WrappedConn returns the wrapped connection object.
func (tc *TracedConn) WrappedConn() driver.Conn {
	return tc.Conn
}

// BeginTx starts a transaction.
//
// The provided context is used until the transaction is committed or rolled back.
// If the context is canceled, the sql package will roll back
// the transaction. Tx.Commit will return an error if the context provided to
// BeginTx is canceled.
//
// The provided TxOptions is optional and may be nil if defaults should be used.
// If a non-default isolation level is used that the driver doesn't support,
// an error will be returned.
func (tc *TracedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) {
	start := time.Now()
	if connBeginTx, ok := tc.Conn.(driver.ConnBeginTx); ok {
		ctx, end := startTraceTask(ctx, QueryTypeBegin)
		defer end()
		tx, err = connBeginTx.BeginTx(ctx, opts)
		tc.tryTrace(ctx, QueryTypeBegin, "", start, err)
		if err != nil {
			return nil, err
		}
		return &tracedTx{tx, tc.traceParams, ctx}, nil
	}
	ctx, end := startTraceTask(ctx, QueryTypeBegin)
	defer end()
	tx, err = tc.Conn.Begin()
	tc.tryTrace(ctx, QueryTypeBegin, "", start, err)
	if err != nil {
		return nil, err
	}
	return &tracedTx{tx, tc.traceParams, ctx}, nil
}

// PrepareContext creates a prepared statement for later queries or executions.
// Multiple queries or executions may be run concurrently from the
// returned statement.
// The caller must call the statement's Close method
// when the statement is no longer needed.
//
// The provided context is used for the preparation of the statement, not for the
// execution of the statement.
func (tc *TracedConn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	start := time.Now()
	mode := tc.cfg.dbmPropagationMode
	if mode == tracer.DBMPropagationModeFull {
		// no context other than service in prepared statements
		mode = tracer.DBMPropagationModeService
	}
	cquery, spanID := tc.injectComments(ctx, query, mode)
	if connPrepareCtx, ok := tc.Conn.(driver.ConnPrepareContext); ok {
		ctx, end := startTraceTask(ctx, QueryTypePrepare)
		defer end()
		stmt, err := connPrepareCtx.PrepareContext(ctx, cquery)
		tc.tryTrace(ctx, QueryTypePrepare, query, start, err, append(withDBMTraceInjectedTag(mode), tracer.WithSpanID(spanID))...)
		if err != nil {
			return nil, err
		}
		return &tracedStmt{Stmt: stmt, traceParams: tc.traceParams, ctx: ctx, query: query}, nil
	}
	ctx, end := startTraceTask(ctx, QueryTypePrepare)
	defer end()
	stmt, err = tc.Prepare(cquery)
	tc.tryTrace(ctx, QueryTypePrepare, query, start, err, append(withDBMTraceInjectedTag(mode), tracer.WithSpanID(spanID))...)
	if err != nil {
		return nil, err
	}
	return &tracedStmt{Stmt: stmt, traceParams: tc.traceParams, ctx: ctx, query: query}, nil
}

// ExecContext executes a query without returning any rows.
// The args are for any placeholder parameters in the query.
func (tc *TracedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, err error) {
	start := time.Now()
	if execContext, ok := tc.Conn.(driver.ExecerContext); ok {
		cquery, spanID := tc.injectComments(ctx, query, tc.cfg.dbmPropagationMode)
		ctx, end := startTraceTask(ctx, QueryTypeExec)
		defer end()
		if err = checkQuerySecurity(ctx, query, tc.driverName); !events.IsSecurityError(err) {
			r, err = execContext.ExecContext(ctx, cquery, args)
		}
		tc.tryTrace(ctx, QueryTypeExec, query, start, err, append(withDBMTraceInjectedTag(tc.cfg.dbmPropagationMode), tracer.WithSpanID(spanID))...)
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
		cquery, spanID := tc.injectComments(ctx, query, tc.cfg.dbmPropagationMode)
		ctx, end := startTraceTask(ctx, QueryTypeExec)
		defer end()
		if err = checkQuerySecurity(ctx, query, tc.driverName); !events.IsSecurityError(err) {
			r, err = execer.Exec(cquery, dargs)
		}
		tc.tryTrace(ctx, QueryTypeExec, query, start, err, append(withDBMTraceInjectedTag(tc.cfg.dbmPropagationMode), tracer.WithSpanID(spanID))...)
		return r, err
	}
	return nil, driver.ErrSkip
}

// Ping verifies the connection to the database is still alive.
func (tc *TracedConn) Ping(ctx context.Context) (err error) {
	start := time.Now()
	if pinger, ok := tc.Conn.(driver.Pinger); ok {
		ctx, end := startTraceTask(ctx, QueryTypePing)
		defer end()
		err = pinger.Ping(ctx)
	}
	tc.tryTrace(ctx, QueryTypePing, "", start, err)
	return err
}

// QueryContext executes a query that returns rows, typically a SELECT.
// The args are for any placeholder parameters in the query.
func (tc *TracedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	start := time.Now()
	if queryerContext, ok := tc.Conn.(driver.QueryerContext); ok {
		cquery, spanID := tc.injectComments(ctx, query, tc.cfg.dbmPropagationMode)
		ctx, end := startTraceTask(ctx, QueryTypeQuery)
		defer end()
		if err = checkQuerySecurity(ctx, query, tc.driverName); !events.IsSecurityError(err) {
			rows, err = queryerContext.QueryContext(ctx, cquery, args)
		}
		tc.tryTrace(ctx, QueryTypeQuery, query, start, err, append(withDBMTraceInjectedTag(tc.cfg.dbmPropagationMode), tracer.WithSpanID(spanID))...)
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
		cquery, spanID := tc.injectComments(ctx, query, tc.cfg.dbmPropagationMode)
		ctx, end := startTraceTask(ctx, QueryTypeQuery)
		defer end()
		if err = checkQuerySecurity(ctx, query, tc.driverName); !events.IsSecurityError(err) {
			rows, err = queryer.Query(cquery, dargs)
		}
		tc.tryTrace(ctx, QueryTypeQuery, query, start, err, append(withDBMTraceInjectedTag(tc.cfg.dbmPropagationMode), tracer.WithSpanID(spanID))...)
		return rows, err
	}
	return nil, driver.ErrSkip
}

// CheckNamedValue is called before passing arguments to the driver
// and is called in place of any ColumnConverter. CheckNamedValue must do type
// validation and conversion as appropriate for the driver.
func (tc *TracedConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := tc.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

var _ driver.SessionResetter = (*TracedConn)(nil)

// ResetSession implements driver.SessionResetter
func (tc *TracedConn) ResetSession(ctx context.Context) error {
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

// providedPeerService returns the peer service tag if provided manually by the user,
// derived from a possible sources (span tags, context, ...)
func (tc *TracedConn) providedPeerService(ctx context.Context) string {
	// This occurs if the user sets peer.service explicitly while creating the connection
	// through the use of sqltrace.WithSpanTags
	if meta, ok := ctx.Value(spanTagsKey).(map[string]string); ok {
		if peerServiceTag, ok := meta[ext.PeerService]; ok {
			return peerServiceTag
		}
	}

	// This occurs if the SQL Connection is opened or registered using
	// WithCustomTags.  This is lower precedence than above since it is
	// less specific
	if len(tc.cfg.tags) > 0 {
		if v, ok := tc.cfg.tags[ext.PeerService].(string); ok {
			return v
		}
	}

	return ""
}

// injectComments returns the query with SQL comments injected according to the comment injection mode along
// with a span ID injected into SQL comments. The returned span ID should be used when the SQL span is created
// following the traced database call.
func (tc *TracedConn) injectComments(ctx context.Context, query string, mode tracer.DBMPropagationMode) (cquery string, spanID uint64) {
	// The sql span only gets created after the call to the database because we need to be able to skip spans
	// when a driver returns driver.ErrSkip. In order to work with those constraints, a new span id is generated and
	// used during SQL comment injection and returned for the sql span to be used later when/if the span
	// gets created.
	var spanCtx ddtrace.SpanContext
	if span, ok := tracer.SpanFromContext(ctx); ok {
		spanCtx = span.Context()
	}

	carrier := tracer.SQLCommentCarrier{Query: query, Mode: mode, DBServiceName: tc.cfg.serviceName, PeerDBHostname: tc.meta[ext.TargetHost], PeerDBName: tc.meta[ext.DBName], PeerService: tc.providedPeerService(ctx)}
	if err := carrier.Inject(spanCtx); err != nil {
		// this should never happen
		log.Warn("contrib/database/sql: failed to inject query comments: %v", err)
	}
	return carrier.Query, carrier.SpanID
}

func withDBMTraceInjectedTag(mode tracer.DBMPropagationMode) []tracer.StartSpanOption {
	if mode == tracer.DBMPropagationModeFull {
		return []tracer.StartSpanOption{tracer.Tag(keyDBMTraceInjected, true)}
	}
	return nil
}

// tryTrace will create a span using the given arguments, but will act as a no-op when err is driver.ErrSkip.
func (tp *traceParams) tryTrace(ctx context.Context, qtype QueryType, query string, startTime time.Time, err error, spanOpts ...ddtrace.StartSpanOption) {
	if err == driver.ErrSkip {
		// Not a user error: driver is telling sql package that an
		// optional interface method is not implemented. There is
		// nothing to trace here.
		// See: https://github.com/DataDog/dd-trace-go/issues/270
		return
	}
	if tp.cfg.ignoreQueryTypes != nil {
		if _, ok := tp.cfg.ignoreQueryTypes[qtype]; ok {
			return
		}
	}
	if _, exists := tracer.SpanFromContext(ctx); tp.cfg.childSpansOnly && !exists {
		return
	}
	dbSystem, _ := normalizeDBSystem(tp.driverName)
	opts := options.Copy(spanOpts...)
	opts = append(opts,
		tracer.ServiceName(tp.cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.StartTime(startTime),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, dbSystem),
	)
	if tp.cfg.tags != nil {
		for key, tag := range tp.cfg.tags {
			opts = append(opts, tracer.Tag(key, tag))
		}
	}
	if !math.IsNaN(tp.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, tp.cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(ctx, tp.cfg.spanName, opts...)
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
	if err != nil && !events.IsSecurityError(err) && (tp.cfg.errCheck == nil || tp.cfg.errCheck(err)) {
		span.SetTag(ext.Error, err)
	}
	span.Finish()
}

func normalizeDBSystem(driverName string) (string, bool) {
	dbSystemMap := map[string]string{
		"mysql":     ext.DBSystemMySQL,
		"postgres":  ext.DBSystemPostgreSQL,
		"pgx":       ext.DBSystemPostgreSQL,
		"sqlserver": ext.DBSystemMicrosoftSQLServer,
	}
	if dbSystem, ok := dbSystemMap[driverName]; ok {
		return dbSystem, true
	}
	return ext.DBSystemOtherSQL, false
}
