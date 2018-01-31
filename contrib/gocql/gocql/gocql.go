// Package gocql provides functions to trace the gocql/gocql package (https://github.com/gocql/gocql).
package gocql

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"

	"github.com/gocql/gocql"
)

// Query inherits from gocql.Query, it keeps the tracer and the context.
type Query struct {
	*gocql.Query
	*params
	traceContext context.Context
}

// Iter inherits from gocql.Iter and contains a span.
type Iter struct {
	*gocql.Iter
	span *tracer.Span
}

// params containes fields and metadata useful for command tracing
type params struct {
	tracer    *tracer.Tracer
	service   string
	keyspace  string
	paginated bool
	query     string
}

// WrapQuery wraps a gocql.Query into a traced Query under the given service name.
//
// TODO(gbbr): Remove tracer arg. when switching to OT.
func WrapQuery(q *gocql.Query, service string, tracer *tracer.Tracer) *Query {
	query := `"` + strings.SplitN(q.String(), "\"", 3)[1] + `"`
	query, err := strconv.Unquote(query)
	if err != nil {
		// An invalid string, so that the trace is not dropped
		// due to having an empty resource
		query = "_"
	}
	tq := &Query{q, &params{
		tracer:  tracer,
		service: service,
		query:   query,
	}, context.Background()}
	tracer.SetServiceInfo(service, ext.CassandraType, ext.AppTypeDB)
	return tq
}

// WithContext rewrites the original function so that ctx can be used for inheritance
func (tq *Query) WithContext(ctx context.Context) *Query {
	tq.traceContext = ctx
	tq.Query.WithContext(ctx)
	return tq
}

// PageState rewrites the original function so that spans are aware of the change.
func (tq *Query) PageState(state []byte) *Query {
	tq.params.paginated = true
	tq.Query = tq.Query.PageState(state)
	return tq
}

// NewChildSpan creates a new span from the params and the context.
func (tq *Query) newChildSpan(ctx context.Context) *tracer.Span {
	p := tq.params
	span := p.tracer.NewChildSpanFromContext(ext.CassandraQuery, ctx)
	span.Type = ext.CassandraType
	span.Service = p.service
	span.Resource = p.query
	span.SetMeta(ext.CassandraPaginated, fmt.Sprintf("%t", p.paginated))
	span.SetMeta(ext.CassandraKeyspace, p.keyspace)
	return span
}

// Exec is rewritten so that it passes by our custom Iter
func (tq *Query) Exec() error {
	return tq.Iter().Close()
}

// MapScan wraps in a span query.MapScan call.
func (tq *Query) MapScan(m map[string]interface{}) error {
	span := tq.newChildSpan(tq.traceContext)
	defer span.Finish()
	err := tq.Query.MapScan(m)
	if err != nil {
		span.SetError(err)
	}
	return err
}

// Scan wraps in a span query.Scan call.
func (tq *Query) Scan(dest ...interface{}) error {
	span := tq.newChildSpan(tq.traceContext)
	defer span.Finish()
	err := tq.Query.Scan(dest...)
	if err != nil {
		span.SetError(err)
	}
	return err
}

// ScanCAS wraps in a span query.ScanCAS call.
func (tq *Query) ScanCAS(dest ...interface{}) (applied bool, err error) {
	span := tq.newChildSpan(tq.traceContext)
	defer span.Finish()
	applied, err = tq.Query.ScanCAS(dest...)
	if err != nil {
		span.SetError(err)
	}
	return applied, err
}

// Iter starts a new span at query.Iter call.
func (tq *Query) Iter() *Iter {
	iter := tq.Query.Iter()
	span := tq.newChildSpan(tq.traceContext)
	span.SetMeta(ext.CassandraRowCount, strconv.Itoa(iter.NumRows()))
	span.SetMeta(ext.CassandraConsistencyLevel, strconv.Itoa(int(tq.GetConsistency())))

	columns := iter.Columns()
	if len(columns) > 0 {
		span.SetMeta(ext.CassandraKeyspace, columns[0].Keyspace)
	}
	tIter := &Iter{iter, span}
	if tIter.Host() != nil {
		tIter.span.SetMeta(ext.TargetHost, tIter.Iter.Host().HostID())
		tIter.span.SetMeta(ext.TargetPort, strconv.Itoa(tIter.Iter.Host().Port()))
		tIter.span.SetMeta(ext.CassandraCluster, tIter.Iter.Host().DataCenter())
	}
	return tIter
}

// Close closes the Iter and finish the span created on Iter call.
func (tIter *Iter) Close() error {
	err := tIter.Iter.Close()
	if err != nil {
		tIter.span.SetError(err)
	}
	tIter.span.Finish()
	return err
}
