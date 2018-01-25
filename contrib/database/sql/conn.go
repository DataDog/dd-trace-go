package sql

import (
	"context"
	"database/sql/driver"
)

var _ driver.Conn = (*tracedConn)(nil)

type tracedConn struct {
	driver.Conn
	*traceParams
}

func (tc *tracedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) {
	span := tc.newChildSpanFromContext(ctx, "Begin", "")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	if connBeginTx, ok := tc.Conn.(driver.ConnBeginTx); ok {
		tx, err = connBeginTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &tracedTx{tx, tc.traceParams, ctx}, nil
	}
	tx, err = tc.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return &tracedTx{tx, tc.traceParams, ctx}, nil
}

func (tc *tracedConn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	span := tc.newChildSpanFromContext(ctx, "Prepare", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	if connPrepareCtx, ok := tc.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := connPrepareCtx.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &tracedStmt{stmt, tc.traceParams, ctx, query}, nil
	}
	stmt, err = tc.Prepare(query)
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
	span := tc.newChildSpanFromContext(ctx, "Exec", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	if execContext, ok := tc.Conn.(driver.ExecerContext); ok {
		return execContext.ExecContext(ctx, query, args)
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
	return tc.Exec(query, dargs)
}

// tracedConn has a Ping method in order to implement the pinger interface
func (tc *tracedConn) Ping(ctx context.Context) (err error) {
	span := tc.newChildSpanFromContext(ctx, "Ping", "")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	if pinger, ok := tc.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (tc *tracedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if queryer, ok := tc.Conn.(driver.Queryer); ok {
		return queryer.Query(query, args)
	}
	return nil, driver.ErrSkip
}

func (tc *tracedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := tc.newChildSpanFromContext(ctx, "Query", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	if queryerContext, ok := tc.Conn.(driver.QueryerContext); ok {
		return queryerContext.QueryContext(ctx, query, args)
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
	return tc.Query(query, dargs)
}
