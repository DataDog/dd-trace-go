package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// RegisterTraced makes a tracing wrapper of the driver available
// by the name "<given name>trace". A DB using this driver should be
// opened using OpenTraced
// If RegisterTraced is called twice with the same name or if driver
// is nil, it panics.
func RegisterTraced(name string, driver driver.Driver) {
	if driver == nil {
		panic("sql: Register driver is nil")
	}

	t := tracer.NewTracer()
	tracedName := name + "trace"
	tracedDriver := NewTracedDriver(name, driver, t)

	sql.Register(tracedName, tracedDriver)
}

// OpenTraced opens a DB using the tracing wrapper of the driver
func OpenTraced(name, service, dsn string) (*sql.DB, error) {
	tracedName := name + "trace"

	// Add tracing info in the DSN to pass it along until the driver.
	// TracedDriver will try to parse this info and use default parameters
	// otherwise
	tracedDSN := TracedDSN{
		service: service,
		dsn:     dsn,
	}
	return sql.Open(tracedName, tracedDSN.Format())
}

type TracedDSN struct {
	service string
	dsn     string
}

func ParseTracedDSN(dsn string) TracedDSN {
	if strings.HasPrefix(dsn, "service=") {
		dsnParts := strings.SplitN(dsn, "|", 2)
		if len(dsnParts) == 2 {
			return TracedDSN{
				service: dsnParts[0][8:],
				dsn:     dsnParts[1],
			}
		}
	}

	return TracedDSN{
		dsn: dsn,
	}
}

func (d TracedDSN) Format() string {
	return fmt.Sprintf("service=%s|%s", d.service, d.dsn)
}

type TracedDriver struct {
	name    string
	parent  driver.Driver
	tracer  *tracer.Tracer
}

func NewTracedDriver(name string, driver driver.Driver, t *tracer.Tracer) TracedDriver {
	return TracedDriver{
		name: name,
		parent: driver,
		tracer: t,
	}
}

func (d TracedDriver) Open(dsn string) (driver.Conn, error) {
	tracedDSN := ParseTracedDSN(dsn)

	// default to SGBD name if no service has been passed in the DSN
	service := d.name
	if tracedDSN.service != "" {
		service = tracedDSN.service
	}

	// Register the service to Datadog tracing API
	d.tracer.SetServiceInfo(service, d.name, ext.AppTypeDB)

	conn, err := d.parent.Open(tracedDSN.dsn)
	if err != nil {
		return nil, err
	}

	return TracedConn{name: d.name, service: service, parent: conn, tracer: d.tracer}, nil
}

type TracedConn struct {
	name    string
	service string
	parent  driver.Conn
	tracer  *tracer.Tracer
}

func (c TracedConn) Prepare(query string) (driver.Stmt, error) {
	return c.parent.Prepare(query)
}

func (c TracedConn) Close() error {
	return c.parent.Close()
}

func (c TracedConn) Begin() (driver.Tx, error) {
	return c.parent.Begin()
}

func (c TracedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) {
	span := c.tracer.NewChildSpanFromContext(c.name + ".connection.begin", ctx)
	span.Service = c.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if connBeginTx, ok := c.parent.(driver.ConnBeginTx); ok {
		tx, err = connBeginTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}

		return TracedTx{name: c.name, service: c.service, parent: tx, tracer: c.tracer, ctx: ctx}, nil
	}

	tx, err = c.parent.Begin()
	if err != nil {
		return nil, err
	}

	return TracedTx{name: c.name, service: c.service, parent: tx, tracer: c.tracer, ctx: ctx}, nil
}

func (c TracedConn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	span := c.tracer.NewChildSpanFromContext(fmt.Sprintf("%s.connection.prepare", c.name), ctx)
	span.Service = c.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if connPrepareCtx, ok := c.parent.(driver.ConnPrepareContext); ok {
		stmt, err := connPrepareCtx.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}

		return TracedStmt{name: c.name, service: c.service, parent: stmt, tracer: c.tracer, ctx: ctx}, nil
	}

	return c.Prepare(query)
}

func (c TracedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if execer, ok := c.parent.(driver.Execer); ok {
		return execer.Exec(query, args)
	}

	return nil, driver.ErrSkip
}

func (c TracedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, err error) {
	span := c.tracer.NewChildSpanFromContext(c.name + ".connection.exec", ctx)
	span.Service = c.service
	span.SetMeta("query", query)
	span.SetMeta("args_length", strconv.Itoa(len(args)))
	span.SetMeta("args", fmt.Sprintf("%v", args))
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if execContext, ok := c.parent.(driver.ExecerContext); ok {
		res, err := execContext.ExecContext(ctx, query, args)
		if err != nil {
			return nil, err
		}

		return TracedResult{name: c.name, service: c.service, parent:res, tracer: c.tracer, ctx: ctx}, nil
	}

	// Fallback implementation
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}

	select {
	default:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return c.Exec(query, dargs)
}

func (c TracedConn) Ping(ctx context.Context) (err error) {
	if pinger, ok := c.parent.(driver.Pinger); ok {
		span := c.tracer.NewChildSpanFromContext(fmt.Sprintf("%s.connection.ping", c.name), ctx)
		defer func() {
			span.SetError(err)
			span.Finish()
		}()

		return pinger.Ping(ctx)
	}

	return nil
}

func (c TracedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if queryer, ok := c.parent.(driver.Queryer); ok {
		return queryer.Query(query, args)
	}

	return nil, driver.ErrSkip
}

func (c TracedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := c.tracer.NewChildSpanFromContext(c.name + ".connection.query", ctx)
	span.Service = c.service
	span.SetMeta("query", query)
	span.SetMeta("args_length", strconv.Itoa(len(args)))
	span.SetMeta("args", fmt.Sprintf("%v", args))
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if queryerContext, ok := c.parent.(driver.QueryerContext); ok {
		rows, err := queryerContext.QueryContext(ctx, query, args)
		if err != nil {
			return nil, err
		}

		return &TracedRows{name: c.name, service: c.service, parent: rows, tracer: c.tracer, ctx: ctx}, nil
	}

	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}

	select {
	default:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return c.Query(query, dargs)
}

type TracedTx struct {
	name    string
	service string
	parent  driver.Tx
	tracer  *tracer.Tracer
	ctx     context.Context
}

func (t TracedTx) Commit() (err error) {
	span := t.tracer.NewChildSpanFromContext(t.name + ".transaction.commit", t.ctx)
	span.Service = t.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return t.parent.Commit()
}

func (t TracedTx) Rollback() (err error) {
	span := t.tracer.NewChildSpanFromContext(t.name + ".transaction.rollback", t.ctx)
	span.Service = t.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return t.parent.Rollback()
}


type TracedStmt struct {
	name    string
	service string
	query   string
	parent  driver.Stmt
	tracer  *tracer.Tracer
	ctx     context.Context
}

func (s TracedStmt) Close() (err error) {
	span := s.tracer.NewChildSpanFromContext(s.name + ".statement.close", s.ctx)
	span.Service = s.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return s.parent.Close()
}

func (s TracedStmt) NumInput() int {
	return s.parent.NumInput()
}

func (s TracedStmt) Exec(args []driver.Value) (res driver.Result, err error) {
	span := s.tracer.NewChildSpanFromContext(s.name + ".statement.exec", s.ctx)
	span.Service = s.service
	span.SetMeta("query", s.query)
	span.SetMeta("args_length", strconv.Itoa(len(args)))
	span.SetMeta("args", fmt.Sprintf("%v", args))
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	res, err = s.parent.Exec(args)
	if err != nil {
		return nil, err
	}

	return TracedResult{name: s.name, service: s.service, parent:res, tracer: s.tracer, ctx: s.ctx}, nil
}

func (s TracedStmt) Query(args []driver.Value) (rows driver.Rows, err error) {
	span := s.tracer.NewChildSpanFromContext(s.name + ".statement.query", s.ctx)
	span.Service = s.service
	span.SetMeta("query", s.query)
	span.SetMeta("args_length", strconv.Itoa(len(args)))
	span.SetMeta("args", fmt.Sprintf("%v", args))
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	rows, err = s.parent.Query(args)
	if err != nil {
		return nil, err
	}

	return &TracedRows{name: s.name, service: s.service, parent: rows, tracer: s.tracer, ctx: s.ctx}, nil
}

func (s TracedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (res driver.Result, err error) {
	span := s.tracer.NewChildSpanFromContext(s.name + ".statement.exec", ctx)
	span.Service = s.service
	span.SetMeta("query", s.query)
	span.SetMeta("args_length", strconv.Itoa(len(args)))
	span.SetMeta("args", fmt.Sprintf("%v", args))
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if stmtExecContext, ok := s.parent.(driver.StmtExecContext); ok {
		res, err = stmtExecContext.ExecContext(ctx, args)
		if err != nil {
			return nil, err
		}

		return TracedResult{name: s.name, service: s.service, parent: res, tracer: s.tracer, ctx: ctx}, nil
	}

	// Fallback implementation
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}

	select {
	default:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return s.Exec(dargs)
}

func (s TracedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := s.tracer.NewChildSpanFromContext(s.name + ".statement.query", s.ctx)
	span.Service = s.service
	span.SetMeta("query", s.query)
	span.SetMeta("args_length", strconv.Itoa(len(args)))
	span.SetMeta("args", fmt.Sprintf("%v", args))
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if stmtQueryContext, ok := s.parent.(driver.StmtQueryContext); ok {
		rows, err = stmtQueryContext.QueryContext(ctx, args)
		if err != nil {
			return nil, err
		}

		return &TracedRows{name: s.name, service: s.service, parent: rows, tracer: s.tracer, ctx: ctx}, nil
	}

	// Fallback implementation
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}

	select {
	default:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return s.Query(dargs)
}


type TracedResult struct {
	name    string
	service string
	parent  driver.Result
	tracer  *tracer.Tracer
	ctx     context.Context
}

func (r TracedResult) LastInsertId() (id int64, err error) {
	span := r.tracer.NewChildSpanFromContext(r.name + ".result.last_insert_id", r.ctx)
	span.Service = r.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return r.parent.LastInsertId()
}

func (r TracedResult) RowsAffected() (num int64, err error) {
	span := r.tracer.NewChildSpanFromContext(r.name + ".result.rows_affected", r.ctx)
	span.Service = r.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return r.parent.RowsAffected()
}


type TracedRows struct {
	name    string
	service string
	rows    int
	parent  driver.Rows
	tracer  *tracer.Tracer
	span    *tracer.Span
	ctx     context.Context
}

func (r TracedRows) Columns() []string {
	return r.parent.Columns()
}

func (r TracedRows) Close() error {
	return r.parent.Close()
}

func (r *TracedRows) Next(dest []driver.Value) (err error) {
	if r.span == nil {
		r.span = r.tracer.NewChildSpanFromContext(r.name + ".rows.iter", r.ctx)
		r.span.Service = r.service
	}

	defer func() {
		if err != nil {
			if err != io.EOF {
				r.span.SetError(err)
			}
			r.span.SetMeta("rows", strconv.Itoa(r.rows))
			r.span.Finish()
		}
		r.rows++
	}()

	return r.parent.Next(dest)
}

// namedValueToValue is a helper function copied from the database/sql package
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
