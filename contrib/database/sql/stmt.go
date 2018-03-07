package sql

import (
	"context"
	"database/sql/driver"
	"errors"

	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/tracer"
)

var _ driver.Stmt = (*tracedStmt)(nil)

// tracedStmt is traced version of sql.Stmt
type tracedStmt struct {
	driver.Stmt
	*traceParams
	ctx   context.Context
	query string
}

// Close sends a span before closing a statement
func (s *tracedStmt) Close() (err error) {
	span := s.newChildSpanFromContext(s.ctx, "Close", "")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return s.Stmt.Close()
}

// ExecContext is needed to implement the driver.StmtExecContext interface
func (s *tracedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (res driver.Result, err error) {
	span := s.newChildSpanFromContext(ctx, "Exec", s.query)
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	if stmtExecContext, ok := s.Stmt.(driver.StmtExecContext); ok {
		return stmtExecContext.ExecContext(ctx, args)
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
	return s.Exec(dargs)
}

// QueryContext is needed to implement the driver.StmtQueryContext interface
func (s *tracedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := s.newChildSpanFromContext(ctx, "Query", s.query)
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	if stmtQueryContext, ok := s.Stmt.(driver.StmtQueryContext); ok {
		return stmtQueryContext.QueryContext(ctx, args)
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
	return s.Query(dargs)
}

// copied from stdlib database/sql package: src/database/sql/ctxutil.go
func namedValueToValue(named []driver.NamedValue) ([]driver.Value, error) {
	dargs := make([]driver.Value, len(named))
	for n, param := range named {
		if len(param.Name) > 0 {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		dargs[n] = param.Value
	}
	return dargs, nil
}
