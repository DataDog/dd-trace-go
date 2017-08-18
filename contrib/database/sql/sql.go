// Package sql provides a traced version of any driver implementing the database/sql/driver interface.
// To trace jmoiron/sqlx, see https://github.com/DataDog/dd-trace-go/contrib/jmoiron/sqlx.
package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	log "github.com/cihub/seelog"

	"github.com/DataDog/dd-trace-go/contrib/database/sql/parsedsn"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// Register takes a driver, registers a traced version of this one and
// then returns the name of the traced driver.
// The last parameter enables you to use a custom tracer.
func Register(driver driver.Driver, t *tracer.Tracer) (traceDriverName string) {
	if driver == nil {
		log.Error("RegisterDriver: driver is nil")
		return ""
	}
	if t == nil {
		t = tracer.DefaultTracer
	}
	driverName := getDriverName(driver)
	traceDriverName = getTraceDriverName(driverName)

	// if no driver is registered under the traceDriverName, we register it
	if !stringInSlice(sql.Drivers(), traceDriverName) {
		d := Driver{driver, t, driverName}
		sql.Register(traceDriverName, d)
		log.Infof("Register %s driver", traceDriverName)
	} else {
		log.Warnf("Register: %s already registered", traceDriverName)
	}
	return traceDriverName
}

// Open will first register the  version of the `driver` if not yet registered and will then open a connection with it.
// This is usually the only function to use when there is no need for the granularity offered by Register and OpenWithService.
// The last parameter is optional and allows you to pass a custom tracer
func Open(driver driver.Driver, dsn, service string, t ...*tracer.Tracer) (*sql.DB, error) {
	// we first register the driver
	traceDriver := Register(driver, getTracer(t))

	// once the  driver is registered, we return the sql.DB to connect to our traced driver
	return OpenWithService(traceDriver, dsn, service)
}

// Open extends the usual API of sql.Open so you can specify the name of the service
// under which the traces will appear in the datadog app.
func OpenWithService(driverName, dsn, service string) (*sql.DB, error) {
	// The service is passed through the DSN
	dsnAndService := newDSNAndService(dsn, service)
	return sql.Open(driverName, dsnAndService)
}

// Driver is a driver we use as a middleware between the database/sql package
// and the driver chosen (e.g. mysql, postgresql...).
// It implements the driver.Driver interface and add the tracing features on top
// of the driver's methods.
type Driver struct {
	driver.Driver
	tracer     *tracer.Tracer
	driverName string
}

// Open returns a Conn so that we can pass all the info we get from the DSN
// all along the tracing
func (d Driver) Open(dsnAndService string) (c driver.Conn, err error) {
	var meta map[string]string
	var conn driver.Conn

	dsn, service := parseDSNAndService(dsnAndService)

	// Register the service to Datadog tracing API
	d.tracer.SetServiceInfo(service, d.driverName, ext.AppTypeDB)

	// Get all kinds of information from the DSN
	meta, err = parsedsn.Parse(d.driverName, dsn)
	if err != nil {
		return nil, err
	}

	conn, err = d.Driver.Open(dsn)
	if err != nil {
		return nil, err
	}

	ti := traceInfo{
		tracer:     d.tracer,
		driverName: d.driverName,
		service:    service,
		meta:       meta,
	}
	return &Conn{conn, ti}, err
}

// traceInfo stores all information relative to the tracing
type traceInfo struct {
	tracer     *tracer.Tracer
	driverName string
	service    string
	resource   string
	meta       map[string]string
}

func (ti traceInfo) getSpan(ctx context.Context, resource string, query ...string) *tracer.Span {
	name := fmt.Sprintf("%s.%s", ti.driverName, "query")
	span := ti.tracer.NewChildSpanFromContext(name, ctx)
	span.Type = ext.SQLType
	span.Service = ti.service
	span.Resource = resource
	if len(query) > 0 {
		span.Resource = query[0]
		span.SetMeta(ext.SQLQuery, query[0])
	}
	for k, v := range ti.meta {
		span.SetMeta(k, v)
	}
	return span
}

type Conn struct {
	driver.Conn
	traceInfo
}

func (c Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) {
	span := c.getSpan(ctx, "Begin")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	if connBeginTx, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = connBeginTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}

		return Tx{tx, c.traceInfo, ctx}, nil
	}

	tx, err = c.Conn.Begin()
	if err != nil {
		return nil, err
	}

	return Tx{tx, c.traceInfo, ctx}, nil
}

func (c Conn) PrepareContext(ctx context.Context, query string) (stmt driver.Stmt, err error) {
	span := c.getSpan(ctx, "Prepare", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	// Check if the driver implements PrepareContext
	if connPrepareCtx, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := connPrepareCtx.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return Stmt{stmt, c.traceInfo, ctx, query}, nil
	}

	// If the driver does not implement PrepareContex (lib/pq for example)
	stmt, err = c.Prepare(query)
	if err != nil {
		return nil, err
	}
	return Stmt{stmt, c.traceInfo, ctx, query}, nil
}

func (c Conn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if execer, ok := c.Conn.(driver.Execer); ok {
		return execer.Exec(query, args)
	}

	return nil, driver.ErrSkip
}

func (c Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (r driver.Result, err error) {
	span := c.getSpan(ctx, "Exec", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if execContext, ok := c.Conn.(driver.ExecerContext); ok {
		res, err := execContext.ExecContext(ctx, query, args)
		if err != nil {
			return nil, err
		}

		return res, nil
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

// Conn has a Ping method in order to implement the pinger interface
func (c Conn) Ping(ctx context.Context) (err error) {
	span := c.getSpan(ctx, "Ping")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if pinger, ok := c.Conn.(driver.Pinger); ok {
		err = pinger.Ping(ctx)
	}

	return err
}

func (c Conn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.Queryer); ok {
		return queryer.Query(query, args)
	}

	return nil, driver.ErrSkip
}

func (c Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := c.getSpan(ctx, "Query", query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if queryerContext, ok := c.Conn.(driver.QueryerContext); ok {
		rows, err := queryerContext.QueryContext(ctx, query, args)
		if err != nil {
			return nil, err
		}

		return rows, nil
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

// Tx is a  version of sql.Tx
type Tx struct {
	driver.Tx
	traceInfo
	ctx context.Context
}

// Commit sends a span at the end of the transaction
func (t Tx) Commit() (err error) {
	span := t.getSpan(t.ctx, "Commit")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return t.Tx.Commit()
}

// Rollback sends a span if the connection is aborted
func (t Tx) Rollback() (err error) {
	span := t.getSpan(t.ctx, "Rollback")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return t.Tx.Rollback()
}

// Stmt is  version of sql.Stmt
type Stmt struct {
	driver.Stmt
	traceInfo
	ctx   context.Context
	query string
}

// Close sends a span before closing a statement
func (s Stmt) Close() (err error) {
	span := s.getSpan(s.ctx, "Close")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	return s.Stmt.Close()
}

// ExecContext is needed to implement the driver.StmtExecContext interface
func (s Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (res driver.Result, err error) {
	span := s.getSpan(s.ctx, "Exec", s.query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if stmtExecContext, ok := s.Stmt.(driver.StmtExecContext); ok {
		res, err = stmtExecContext.ExecContext(ctx, args)
		if err != nil {
			return nil, err
		}

		return res, nil
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

// QueryContext is needed to implement the driver.StmtQueryContext interface
func (s Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (rows driver.Rows, err error) {
	span := s.getSpan(s.ctx, "Query", s.query)
	defer func() {
		span.SetError(err)
		span.Finish()
	}()

	if stmtQueryContext, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err = stmtQueryContext.QueryContext(ctx, args)
		if err != nil {
			return nil, err
		}

		return rows, nil
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
