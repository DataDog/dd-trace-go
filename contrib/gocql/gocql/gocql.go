// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package gocql provides functions to trace the gocql/gocql package (https://github.com/gocql/gocql).
package gocql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gocql/gocql"

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gocql/gocql"
)

// Query inherits from gocql.Query, it keeps the tracer and the context.
type Query struct {
	*gocql.Query
	*params
	ctx context.Context
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
}

// WrapQuery wraps a gocql.Query into a traced Query under the given service name.
// Note that the returned Query structure embeds the original gocql.Query structure.
// This means that any method returning the query for chaining that is not part
// of this package's Query structure should be called before WrapQuery, otherwise
// the tracing context could be lost.
//
// To be more specific: it is ok (and recommended) to use and chain the return value
// of `WithContext` and `PageState` but not that of `Consistency`, `Trace`,
// `Observer`, etc.
func WrapQuery(q *gocql.Query, opts ...WrapOption) *Query {
	cfg := new(queryConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.resourceName == "" {
		q := `"` + strings.SplitN(q.String(), "\"", 3)[1] + `"`
		q, err := strconv.Unquote(q)
		if err != nil {
			// avoid having an empty resource as it will cause the trace
			// to be dropped.
			q = "_"
		}
		cfg.resourceName = q
	}
	tq := &Query{q, &params{config: cfg}, context.Background()}
	return tq
}

// WithContext adds the specified context to the traced Query structure.
func (tq *Query) WithContext(ctx context.Context) *Query {
	tq.ctx = ctx
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
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeCassandra),
		tracer.ServiceName(p.config.serviceName),
		tracer.ResourceName(p.config.resourceName),
		tracer.Tag(ext.CassandraPaginated, fmt.Sprintf("%t", p.paginated)),
		tracer.Tag(ext.CassandraKeyspace, p.keyspace),
		tracer.MeasureSpan(),
	}
	if !math.IsNaN(p.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(ctx, ext.CassandraQuery, opts...)
	return span
}

func (tq *Query) finishSpan(span ddtrace.Span, err error) {
	if tq.params.config.noDebugStack {
		span.Finish(tracer.WithError(err), tracer.NoDebugStack())
	} else {
		span.Finish(tracer.WithError(err))
	}
}

// Exec is rewritten so that it passes by our custom Iter
func (tq *Query) Exec() error {
	return tq.Iter().Close()
}

// MapScan wraps in a span query.MapScan call.
func (tq *Query) MapScan(m map[string]interface{}) error {
	span := tq.newChildSpan(tq.ctx)
	err := tq.Query.MapScan(m)
	tq.finishSpan(span, err)
	return err
}

// Scan wraps in a span query.Scan call.
func (tq *Query) Scan(dest ...interface{}) error {
	span := tq.newChildSpan(tq.ctx)
	err := tq.Query.Scan(dest...)
	tq.finishSpan(span, err)
	return err
}

// ScanCAS wraps in a span query.ScanCAS call.
func (tq *Query) ScanCAS(dest ...interface{}) (applied bool, err error) {
	span := tq.newChildSpan(tq.ctx)
	applied, err = tq.Query.ScanCAS(dest...)
	tq.finishSpan(span, err)
	return applied, err
}

// Iter starts a new span at query.Iter call.
func (tq *Query) Iter() *Iter {
	span := tq.newChildSpan(tq.ctx)
	iter := tq.Query.Iter()
	span.SetTag(ext.CassandraRowCount, strconv.Itoa(iter.NumRows()))
	span.SetTag(ext.CassandraConsistencyLevel, tq.GetConsistency().String())

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
