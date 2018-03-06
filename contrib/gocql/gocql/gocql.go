// Package gocql provides functions to trace the gocql/gocql package (https://github.com/gocql/gocql).
package gocql

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/ddtrace"
	"github.com/DataDog/dd-trace-go/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/ddtrace/tracer"

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
	span ddtrace.Span
}

// params containes fields and metadata useful for command tracing
type params struct {
	config    *queryConfig
	keyspace  string
	paginated bool
	query     string
}

// WrapQuery wraps a gocql.Query into a traced Query under the given service name.
func WrapQuery(q *gocql.Query, opts ...WrapOption) *Query {
	cfg := new(queryConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	query := `"` + strings.SplitN(q.String(), "\"", 3)[1] + `"`
	query, err := strconv.Unquote(query)
	if err != nil {
		// An invalid string, so that the trace is not dropped
		// due to having an empty resource
		query = "_"
	}
	tq := &Query{q, &params{
		config: cfg,
		query:  query,
	}, context.Background()}
	tracer.SetServiceInfo(cfg.serviceName, ext.CassandraType, ext.AppTypeDB)
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
func (tq *Query) newChildSpan(ctx context.Context) ddtrace.Span {
	p := tq.params
	span, _ := tracer.StartSpanFromContext(ctx, ext.CassandraQuery,
		tracer.SpanType(ext.CassandraType),
		tracer.ServiceName(p.config.serviceName),
		tracer.ResourceName(p.query),
		tracer.Tag(ext.CassandraPaginated, fmt.Sprintf("%t", p.paginated)),
		tracer.Tag(ext.CassandraKeyspace, p.keyspace),
	)
	return span
}

// Exec is rewritten so that it passes by our custom Iter
func (tq *Query) Exec() error {
	return tq.Iter().Close()
}

// MapScan wraps in a span query.MapScan call.
func (tq *Query) MapScan(m map[string]interface{}) error {
	span := tq.newChildSpan(tq.traceContext)
	err := tq.Query.MapScan(m)
	span.Finish(tracer.WithError(err))
	return err
}

// Scan wraps in a span query.Scan call.
func (tq *Query) Scan(dest ...interface{}) error {
	span := tq.newChildSpan(tq.traceContext)
	err := tq.Query.Scan(dest...)
	span.Finish(tracer.WithError(err))
	return err
}

// ScanCAS wraps in a span query.ScanCAS call.
func (tq *Query) ScanCAS(dest ...interface{}) (applied bool, err error) {
	span := tq.newChildSpan(tq.traceContext)
	applied, err = tq.Query.ScanCAS(dest...)
	span.Finish(tracer.WithError(err))
	return applied, err
}

// Iter starts a new span at query.Iter call.
func (tq *Query) Iter() *Iter {
	iter := tq.Query.Iter()
	span := tq.newChildSpan(tq.traceContext)
	span.SetTag(ext.CassandraRowCount, strconv.Itoa(iter.NumRows()))
	span.SetTag(ext.CassandraConsistencyLevel, strconv.Itoa(int(tq.GetConsistency())))

	columns := iter.Columns()
	if len(columns) > 0 {
		span.SetTag(ext.CassandraKeyspace, columns[0].Keyspace)
	}
	tIter := &Iter{iter, span}
	if tIter.Host() != nil {
		tIter.span.SetTag(ext.TargetHost, tIter.Iter.Host().HostID())
		tIter.span.SetTag(ext.TargetPort, strconv.Itoa(tIter.Iter.Host().Port()))
		tIter.span.SetTag(ext.CassandraCluster, tIter.Iter.Host().DataCenter())
	}
	return tIter
}

// Close closes the Iter and finish the span created on Iter call.
func (tIter *Iter) Close() error {
	err := tIter.Iter.Close()
	if err != nil {
		tIter.span.SetTag(ext.Error, err)
	}
	tIter.span.Finish()
	return err
}
