package sqltraced

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	log "github.com/cihub/seelog"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// Register takes a driver and registers a traced version of this one.
// However, user must take care not using the same name of the original driver
// E.g. setting the name to "mysql" for tracing the mysql driver will make the program
// panic. You can use the name "MySQL" to avoid that.
func Register(name, service string, driver driver.Driver, trc *tracer.Tracer) {
	log.Infof("RegisterTracedDriver: name=%s, service=%s", name, service)

	if driver == nil {
		log.Error("RegisterTracedDriver: driver is nil")
		return
	}
	if trc == nil {
		trc = tracer.DefaultTracer
	}

	ti := TraceInfo{
		name:    name,
		service: service,
		tracer:  trc,
	}
	td := TracedDriver{driver, ti}

	if !stringInSlice(sql.Drivers(), name) {
		sql.Register(name, td)
	} else {
		log.Errorf("RegisterTracedDriver: %s already registered", name)
	}
}

// TraceInfo stores all information relative to the tracing
type TraceInfo struct {
	name     string
	service  string
	resource string
	tracer   *tracer.Tracer
	meta     map[string]string
}

func (ti TraceInfo) getSpan(ctx context.Context, suffix string, query ...string) *tracer.Span {
	name := fmt.Sprintf("%s.%s", strings.ToLower(ti.name), suffix)
	span := ti.tracer.NewChildSpanFromContext(name, ctx)
	span.Type = ext.SQLType
	span.Service = ti.service
	if len(query) > 0 {
		span.Resource = query[0]
		span.SetMeta(ext.SQLQuery, query[0])
	}
	for k, v := range ti.meta {
		span.SetMeta(k, v)
	}
	return span
}

// TracedDriver is a driver we use as a middleware between the database/sql package
// and the driver chosen (e.g. mysql, postgresql...).
// It implements the driver.Driver interface and add the tracing features on top
// of the driver's methods.
type TracedDriver struct {
	driver.Driver
	TraceInfo
}

func (td TracedDriver) Open(dsn string) (c driver.Conn, err error) {
	var meta map[string]string
	var conn driver.Conn

	// Register the service to Datadog tracing API
	td.tracer.SetServiceInfo(td.service, td.name, ext.AppTypeDB)

	// Get all kinds of information from the DSN
	driverType := fmt.Sprintf("%s", reflect.TypeOf(td.Driver))
	meta, err = parseDSN(driverType, dsn)
	if err != nil {
		return nil, err
	}

	conn, err = td.Driver.Open(dsn)
	if err != nil {
		return nil, err
	}

	ti := TraceInfo{
		name:    td.name,
		service: td.service,
		tracer:  td.tracer,
		meta:    meta,
	}
	return &TracedConn{conn, ti}, err
}

type TracedConn struct {
	driver.Conn
	TraceInfo
}

func (tc TracedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) {
	span := tc.getSpan(ctx, "begin")
	span.Resource = "Begin"
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if connBeginTx, ok := tc.Conn.(driver.ConnBeginTx); ok {
		tx, err = connBeginTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}

		return TracedTx{tx, tc.TraceInfo, ctx}, nil
	}

	tx, err = tc.Conn.Begin()
	if err != nil {
		return nil, err
	}

	return TracedTx{tx, tc.TraceInfo, ctx}, nil
}

func (tc TracedConn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	span := tc.getSpan(ctx, "prepare", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if connPrepareCtx, ok := tc.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := connPrepareCtx.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}

		return TracedStmt{name: tc.name, service: tc.service, parent: stmt, tracer: tc.tracer, ctx: ctx}, nil
	}

	return tc.Prepare(query)
}

func (tc TracedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if execer, ok := tc.Conn.(driver.Execer); ok {
		return execer.Exec(query, args)
	}

	return nil, driver.ErrSkip
}

func (tc TracedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, err error) {
	span := tc.getSpan(ctx, "exec", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if execContext, ok := tc.Conn.(driver.ExecerContext); ok {
		res, err := execContext.ExecContext(ctx, query, args)
		if err != nil {
			return nil, err
		}

		return TracedResult{name: tc.name, service: tc.service, parent: res, tracer: tc.tracer, ctx: ctx}, nil
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

	return tc.Exec(query, dargs)
}

// TracedConn has a Ping method in order to implement the pinger interface
func (tc TracedConn) Ping(ctx context.Context) (err error) {
	span := tc.getSpan(ctx, "ping")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if pinger, ok := tc.Conn.(driver.Pinger); ok {
		err = pinger.Ping(ctx)
	}

	return err
}

func (c TracedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.Queryer); ok {
		return queryer.Query(query, args)
	}

	return nil, driver.ErrSkip
}

func (c TracedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := c.getSpan(ctx, "query", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if queryerContext, ok := c.Conn.(driver.QueryerContext); ok {
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
	driver.Tx
	TraceInfo
	ctx context.Context
}

func (t TracedTx) Commit() (err error) {
	span := t.getSpan(t.ctx, "commit")
	span.Resource = "Commit"
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return t.Tx.Commit()
}

func (t TracedTx) Rollback() (err error) {
	span := t.getSpan(t.ctx, "rollback")
	span.Resource = "Rollback"
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return t.Tx.Rollback()
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
	span := s.tracer.NewChildSpanFromContext(s.name+".statement.close", s.ctx)
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
	name := fmt.Sprintf("%s.statement.exec", s.name)
	span := getSpan(name, s.service, s.query, args, s.tracer, s.ctx)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	res, err = s.parent.Exec(args)
	if err != nil {
		return nil, err
	}

	return TracedResult{name: s.name, service: s.service, parent: res, tracer: s.tracer, ctx: s.ctx}, nil
}

func (s TracedStmt) Query(args []driver.Value) (rows driver.Rows, err error) {
	name := fmt.Sprintf("%s.statement.query", s.name)
	span := getSpan(name, s.service, s.query, args, s.tracer, s.ctx)
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
	name := fmt.Sprintf("%s.statement.exec", s.name)
	span := getSpan(name, s.service, s.query, args, s.tracer, s.ctx)
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
	name := fmt.Sprintf("%s.statement.query", s.name)
	span := getSpan(name, s.service, s.query, args, s.tracer, ctx)
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
	span := r.tracer.NewChildSpanFromContext(r.name+".result.last_insert_id", r.ctx)
	span.Service = r.service
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return r.parent.LastInsertId()
}

func (r TracedResult) RowsAffected() (num int64, err error) {
	span := r.tracer.NewChildSpanFromContext(r.name+".result.rows_affected", r.ctx)
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
		r.span = r.tracer.NewChildSpanFromContext(r.name+".rows.iter", r.ctx)
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
