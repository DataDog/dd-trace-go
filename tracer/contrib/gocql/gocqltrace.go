// Package gocqltrace provides tracing for the Cassandra Gocql client (https://github.com/gocql/gocql)
package gocqltrace

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/furmmon/gocql"
	"strconv"
	"strings"
)

// TracedQuery inherits from gocql.Query, it keeps the tracer and the context.
type TracedQuery struct {
	*gocql.Query
	p            traceParams
	traceContext context.Context
}

// TracedIter inherits from gocql.Iter and contains a span.
type TracedIter struct {
	*gocql.Iter
	span *tracer.Span
}

// traceParams containes fields and metadata useful for command tracing
type traceParams struct {
	tracer      *tracer.Tracer
	service     string
	keyspace    string
	paginated   string
	consistancy string
	query       string
}

// Without wrapper code

type TracedBatch struct {
	*gocql.Batch
	p            traceParams
	traceContext context.Context
}

type TracedClusterConfig struct {
	*gocql.ClusterConfig
}

type TracedSession struct {
	*gocql.Session
	p traceParams
}

func NewTracedCluster(hosts ...string) *TracedClusterConfig {
	tcfg := &TracedClusterConfig{gocql.NewCluster(hosts...)}
	return tcfg
}

// NewTracedClusterConfig necessary to use CreateTracedSession.
func NewTracedClusterConfig(clg *gocql.ClusterConfig) *TracedClusterConfig {
	return &TracedClusterConfig{clg}
}

// CreateTracedSession creates from a TracedClusterConfig a new TracedSession.
func (tcfg *TracedClusterConfig) CreateTracedSession(service string, tracer *tracer.Tracer) (*TracedSession, error) {
	return NewTracedSession(service, tracer, *tcfg.ClusterConfig)
}

// NewTracedSession allows to add service and tracer on a new TracedSession from config.
func NewTracedSession(service string, tracer *tracer.Tracer, cfg gocql.ClusterConfig) (*TracedSession, error) {
	s, err := gocql.NewSession(cfg)
	ts := &TracedSession{s, traceParams{tracer, service, cfg.Keyspace, "false", "", ""}}
	return ts, err
}

// Query creates a TracedQuery.
func (ts *TracedSession) Query(stmt string, values ...interface{}) *TracedQuery {
	q := ts.Session.Query(stmt, values...)
	p := ts.p
	p.query = stmt
	tq := &TracedQuery{q, p, context.Background()}
	return tq
}

// Bind creates a TracedQuery from a session.
func (ts *TracedSession) Bind(stmt string, b func(q *gocql.QueryInfo) ([]interface{}, error)) *TracedQuery {
	q := ts.Session.Bind(stmt, b)
	tq := &TracedQuery{q, ts.p, context.Background()}
	return tq
}

// Wrapper

// TraceQuery wraps a gocql.Query into a TracedQuery
func TraceQuery(service string, tracer *tracer.Tracer, q *gocql.Query) *TracedQuery {
	string_query := strings.SplitN(q.String(), "\"", 3)[1]
	q.NoSkipMetadata()
	tq := &TracedQuery{q, traceParams{tracer, service, "", "false", strconv.Itoa(int(q.GetConsistency())), string_query}, context.Background()}
	tracer.SetServiceInfo(service, ext.CassandraType, ext.AppTypeDB)
	return tq
}

// Common code

// WithContext rewrites the original function so that ctx can be used for inheritance
func (tq *TracedQuery) WithContext(ctx context.Context) *TracedQuery {
	tq.traceContext = ctx
	tq.Query.WithContext(ctx)
	return tq
}

func (tq *TracedQuery) PageState(state []byte) *TracedQuery {
	tq.p.paginated = "true"
	tq.Query = tq.Query.PageState(state)
	return tq
}

// NewChildSpan creates a new span from the traceParams and the context.
func (tq *TracedQuery) NewChildSpan(ctx context.Context) *tracer.Span {
	span := tq.p.tracer.NewChildSpanFromContext(ext.CassandraQuery, ctx)
	span.Type = ext.CassandraType
	span.Service = tq.p.service
	span.Resource = tq.p.query
	span.SetMeta(ext.CassandraPaginated, tq.p.paginated)
	span.SetMeta(ext.CassandraKeyspace, tq.p.keyspace)
	return span
}

// Exec is rewritten so that it passes by our custom Iter
func (tq *TracedQuery) Exec() error {
	return tq.Iter().Close()
}

// MapScan wraps in a span query.MapScan call.
func (tq *TracedQuery) MapScan(m map[string]interface{}) error {
	span := tq.NewChildSpan(tq.traceContext)
	defer span.Finish()
	err := tq.Query.MapScan(m)
	if err != nil {
		span.SetError(err)
	}
	return err
}

// Scan wraps in a span query.Scan call.
func (tq *TracedQuery) Scan(dest ...interface{}) error {
	span := tq.NewChildSpan(tq.traceContext)
	defer span.Finish()
	err := tq.Query.Scan(dest...)
	if err != nil {
		span.SetError(err)
	}
	return err
}

// ScanCAS wraps in a span query.ScanCAS call.
func (tq *TracedQuery) ScanCAS(dest ...interface{}) (applied bool, err error) {
	span := tq.NewChildSpan(tq.traceContext)
	defer span.Finish()
	applied, err = tq.Query.ScanCAS(dest...)
	if err != nil {
		span.SetError(err)
	}
	return applied, err
}

// Iter starts a new span at query.Iter call.
func (tq *TracedQuery) Iter() *TracedIter {
	span := tq.NewChildSpan(tq.traceContext)
	iter := tq.Query.Iter()
	span.SetMeta(ext.CassandraRowCount, strconv.Itoa(iter.NumRows()))
	span.SetMeta(ext.CassandraConsistencyLevel, strconv.Itoa(int(tq.GetConsistency())))

	columns := iter.Columns()
	if len(columns) > 0 {
		span.SetMeta(ext.CassandraKeyspace, columns[0].Keyspace)
	} else {
	}
	tIter := &TracedIter{iter, span}
	if tIter.Host() != nil {
		tIter.span.SetMeta(ext.TargetHost, tIter.Iter.Host().HostID())
		tIter.span.SetMeta(ext.TargetPort, strconv.Itoa(tIter.Iter.Host().Port()))
		tIter.span.SetMeta(ext.CassandraCluster, tIter.Iter.Host().DataCenter())

	}
	return tIter
}

// Close closes the TracedIter and finish the span created on Iter call.
func (tIter *TracedIter) Close() error {
	columns := tIter.Iter.Columns()
	if len(columns) > 0 {
		tIter.span.SetMeta(ext.CassandraKeyspace, columns[0].Keyspace)
	}
	err := tIter.Iter.Close()
	if err != nil {
		tIter.span.SetError(err)
	}
	if tIter.Host() != nil {
		tIter.span.SetMeta(ext.TargetHost, tIter.Iter.Host().Peer().String())
		tIter.span.SetMeta(ext.TargetPort, strconv.Itoa(tIter.Iter.Host().Port()))
		tIter.span.SetMeta(ext.CassandraCluster, tIter.Iter.Host().DataCenter())
	}
	tIter.span.Finish()
	return err
}
