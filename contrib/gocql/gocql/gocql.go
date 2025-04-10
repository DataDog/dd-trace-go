// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gocql provides functions to trace the gocql/gocql package (https://github.com/gocql/gocql).
package gocql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gocql/gocql"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"

	"github.com/gocql/gocql"
)

// ClusterConfig embeds gocql.ClusterConfig and keeps information relevant to tracing.
type ClusterConfig = v2.ClusterConfig

// NewCluster calls gocql.NewCluster and returns a wrapped instrumented version of it.
//
// Deprecated: use the Observer based method CreateTracedSession instead, which allows to use
// native gocql types instead of wrapped types.
func NewCluster(hosts []string, opts ...WrapOption) *ClusterConfig {
	return v2.NewCluster(hosts, opts...)
}

// Session embeds gocql.Session and keeps information relevant to tracing.
type Session = v2.Session

// Query inherits from gocql.Query, it keeps the tracer and the context.
type Query = v2.Query

// Batch inherits from gocql.Batch, it keeps the tracer and the context.
type Batch = v2.Batch

// WrapQuery wraps a gocql.Query into a traced Query under the given service name.
// Note that the returned Query structure embeds the original gocql.Query structure.
// This means that any method returning the query for chaining that is not part
// of this package's Query structure should be called before WrapQuery, otherwise
// the tracing context could be lost.
//
// To be more specific: it is ok (and recommended) to use and chain the return value
// of `WithContext` and `PageState` but not that of `Consistency`, `Trace`,
// `Observer`, etc.
//
// Deprecated: use the Observer based method CreateTracedSession instead, which allows to use
// native gocql types instead of wrapped types.
func WrapQuery(q *gocql.Query, opts ...WrapOption) *Query {
	return v2.WrapQuery(q, opts...)
}

// Iter inherits from gocql.Iter and contains a span.
type Iter = v2.Iter

// Scanner inherits from a gocql.Scanner derived from an Iter
type Scanner = v2.Scanner

// WrapBatch wraps a gocql.Batch into a traced Batch under the given service name.
// Note that the returned Batch structure embeds the original gocql.Batch structure.
// This means that any method returning the batch for chaining that is not part
// of this package's Batch structure should be called before WrapBatch, otherwise
// the tracing context could be lost.
//
// To be more specific: it is ok (and recommended) to use and chain the return value
// of `WithContext` and `WithTimestamp` but not that of `SerialConsistency`, `Trace`,
// `Observer`, etc.
//
// Deprecated: use the Observer based method CreateTracedSession instead, which allows to use
// native gocql types instead of wrapped types.
func WrapBatch(b *gocql.Batch, opts ...WrapOption) *Batch {
	return v2.WrapBatch(b, opts...)
}
