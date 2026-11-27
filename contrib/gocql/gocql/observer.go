// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gocql

import (
	"context"
	"strings"

	"github.com/gocql/gocql"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// CreateTracedSession returns a new session augmented with tracing.
func CreateTracedSession(cluster *gocql.ClusterConfig, opts ...WrapOption) (*gocql.Session, error) {
	obs := NewObserver(cluster, opts...)
	cfg := obs.cfg

	if cfg.traceQuery {
		cluster.QueryObserver = obs
	} else if automatic(cluster.QueryObserver) {
		cluster.QueryObserver = nil
	}
	if cfg.traceBatch {
		cluster.BatchObserver = obs
	} else if automatic(cluster.BatchObserver) {
		cluster.BatchObserver = nil
	}
	if cfg.traceConnect {
		cluster.ConnectObserver = obs
	} else if automatic(cluster.ConnectObserver) {
		cluster.ConnectObserver = nil
	}
	// In an otelc build, a hook on gocql's CreateSession sets the observers
	// this function left nil. Calling NewSession, which CreateSession only
	// forwards to, keeps them unset.
	return gocql.NewSession(*cluster)
}

// NewObserver creates a new Observer to trace gocql.
// This method is useful in case you want to attach the observer to individual traces / batches instead of instrumenting
// the whole client.
func NewObserver(cluster *gocql.ClusterConfig, opts ...WrapOption) *Observer {
	cfg := defaultConfig()
	for _, fn := range opts {
		fn.apply(cfg)
	}
	return &Observer{
		cfg:                  cfg,
		clusterContactPoints: strings.Join(cluster.Hosts, ","),
	}
}

// automaticObserver is implemented by the observers that the otelc rules set,
// in contrib/gocql/gocql/otelc. CreateTracedSession removes such an observer
// for an operation its options leave untraced. The otelc module cannot see
// this interface, so the integration tests check that both sides agree.
type automaticObserver interface {
	DDOtelcAutomatic()
}

func automatic(o any) bool {
	_, ok := o.(automaticObserver)
	return ok
}

var (
	_ gocql.QueryObserver   = (*Observer)(nil)
	_ gocql.BatchObserver   = (*Observer)(nil)
	_ gocql.ConnectObserver = (*Observer)(nil)
)

// Observer implements gocql observer interfaces to support tracing.
type Observer struct {
	cfg                  *config
	clusterContactPoints string
}

// ObserveQuery implements gocql.QueryObserver.
func (o *Observer) ObserveQuery(ctx context.Context, query gocql.ObservedQuery) {
	p := params{
		config:               o.cfg,
		keyspace:             query.Keyspace,
		skipPaginated:        true,
		clusterContactPoints: o.clusterContactPoints,
		hostInfo:             query.Host,
		startTime:            query.Start,
		finishTime:           query.End,
	}
	span := startQuerySpan(ctx, p)
	resource := o.cfg.resourceName
	if resource == "" {
		resource = query.Statement
	}
	span.SetTag(ext.ResourceName, resource)
	span.SetTag(ext.CassandraRowCount, query.Rows)
	finishSpan(span, query.Err, p)
}

// ObserveBatch implements gocql.BatchObserver.
func (o *Observer) ObserveBatch(ctx context.Context, batch gocql.ObservedBatch) {
	p := params{
		config:               o.cfg,
		keyspace:             batch.Keyspace,
		skipPaginated:        true,
		clusterContactPoints: o.clusterContactPoints,
		hostInfo:             batch.Host,
		startTime:            batch.Start,
		finishTime:           batch.End,
	}
	span := startBatchSpan(ctx, p)
	finishSpan(span, batch.Err, p)
}

// ObserveConnect implements gocql.ConnectObserver.
func (o *Observer) ObserveConnect(connect gocql.ObservedConnect) {
	p := params{
		config:               o.cfg,
		clusterContactPoints: o.clusterContactPoints,
		hostInfo:             connect.Host,
		startTime:            connect.Start,
		finishTime:           connect.End,
	}
	opts := commonStartSpanOptions(p)
	for k, v := range o.cfg.customTags {
		opts = append(opts, tracer.Tag(k, v))
	}
	span := tracer.StartSpan("cassandra.connect", opts...)
	finishSpan(span, connect.Err, p)
}
