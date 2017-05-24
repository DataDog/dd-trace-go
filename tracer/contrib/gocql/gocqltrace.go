// Package gocqltrace provides tracing for the Cassandra Gocql client (https://github.com/gocql/gocql)
package gocqltrace

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
	"github.com/gocql/gocql"
	"strconv"
	"strings"
)

type TracedQuery struct {
	*gocql.Query
	p            traceParams
	traceContext context.Context
}

type TracedIter struct {
	*gocql.Iter
	span *tracer.Span
}

type traceParams struct {
	tracer      *tracer.Tracer
	service     string
	port        string
	keyspace    string
	paginated   string
	consistancy string
	query       string
}

// Without wrapper code

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

func NewTracedClusterConfig(clg *gocql.ClusterConfig) *TracedClusterConfig {
	return &TracedClusterConfig{clg}
}

func (tcfg *TracedClusterConfig) CreateTracedSession(service string, tracer *tracer.Tracer) (*TracedSession, error) {
	return NewTracedSession(service, tracer, *tcfg.ClusterConfig)
}

func NewTracedSession(service string, tracer *tracer.Tracer, cfg gocql.ClusterConfig) (*TracedSession, error) {
	s, err := gocql.NewSession(cfg)
	ts := &TracedSession{s, traceParams{tracer, service, strconv.Itoa(cfg.Port), cfg.Keyspace, "false", "", ""}}
	return ts, err
}

func (ts *TracedSession) Query(stmt string, values ...interface{}) *TracedQuery {
	q := ts.Session.Query(stmt, values...)
	p := ts.p
	p.query = stmt
	tq := &TracedQuery{q, p, context.Background()}
	return tq
}

func (ts *TracedSession) Bind(stmt string, b func(q *gocql.QueryInfo) ([]interface{}, error)) *TracedQuery {
	q := ts.Session.Bind(stmt, b)
	tq := &TracedQuery{q, ts.p, context.Background()}
	return tq
}

// Wrapper

func TraceQuery(service string, tracer *tracer.Tracer, q *gocql.Query) *TracedQuery {
	string_query := strings.SplitN(q.String(), "\"", 3)[1]
	q.NoSkipMetadata()
	tq := &TracedQuery{q, traceParams{tracer, service, "", "", "false", strconv.Itoa(int(q.GetConsistency())), string_query}, context.Background()}
	tracer.SetServiceInfo(service, ext.CassandraType, ext.AppTypeDB)
	return tq
}

// Common code

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

func (tq *TracedQuery) NewChildSpan(ctx context.Context) *tracer.Span {
	span := tq.p.tracer.NewChildSpanFromContext(ext.CassandraQuery, ctx)
	span.Type = ext.CassandraType
	span.Service = tq.p.service
	span.Resource = tq.p.query
	span.SetMeta(ext.CassandraPaginated, tq.p.paginated)
	span.SetMeta(ext.CassandraKeyspace, tq.p.keyspace)
	span.SetMeta(ext.TargetPort, tq.p.port)
	return span
}

func (tq *TracedQuery) Exec() error {
	return tq.Iter().Close()
}

func (tq *TracedQuery) MapScan(m map[string]interface{}) error {
	span := tq.NewChildSpan(tq.traceContext)
	defer span.Finish()
	err := tq.Query.MapScan(m)
	if err != nil {
		span.SetError(err)
	}
	return err
}

func (tq *TracedQuery) Scan(dest ...interface{}) error {
	span := tq.NewChildSpan(tq.traceContext)
	defer span.Finish()
	err := tq.Query.Scan(dest...)
	if err != nil {
		span.SetError(err)
	}
	return err
}

func (tq *TracedQuery) ScanCAS(dest ...interface{}) (applied bool, err error) {
	span := tq.NewChildSpan(tq.traceContext)
	defer span.Finish()
	applied, err = tq.Query.ScanCAS(dest...)
	if err != nil {
		span.SetError(err)
	}
	return applied, err
}

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
		tIter.span.SetMeta(ext.TargetHost, tIter.Iter.Host().HostID())
		tIter.span.SetMeta(ext.TargetPort, strconv.Itoa(tIter.Iter.Host().Port()))
		tIter.span.SetMeta(ext.CassandraCluster, tIter.Iter.Host().DataCenter())
	}
	tIter.span.Finish()
	return err
}
