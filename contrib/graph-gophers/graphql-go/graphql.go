// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package graphql provides functions to trace the graph-gophers/graphql-go package (https://github.com/graph-gophers/graphql-go).
//
// We use the tracing mechanism available in the
// https://godoc.org/github.com/graph-gophers/graphql-go/trace subpackage.
// Create a new Tracer with `NewTracer` and pass it as an additional option to
// `MustParseSchema`.
package graphql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/graph-gophers/graphql-go"

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2"

	"github.com/graph-gophers/graphql-go/introspection"
	"github.com/graph-gophers/graphql-go/trace/tracer"
)

// A Tracer implements the graphql-go/trace.Tracer interface by sending traces
// to the Datadog tracer.
type Tracer struct {
	tracer.Tracer
}

var _ tracer.Tracer = (*Tracer)(nil)

// TraceQuery traces a GraphQL query.
func (t *Tracer) TraceQuery(ctx context.Context, queryString, operationName string, variables map[string]interface{}, _ map[string]*introspection.Type) (context.Context, tracer.QueryFinishFunc) {
	return t.Tracer.TraceQuery(ctx, queryString, operationName, variables, nil)
}

// TraceField traces a GraphQL field access.
func (t *Tracer) TraceField(ctx context.Context, _, typeName, fieldName string, trivial bool, arguments map[string]interface{}) (context.Context, tracer.FieldFinishFunc) {
	return t.Tracer.TraceField(ctx, "", typeName, fieldName, trivial, arguments)
}

// NewTracer creates a new Tracer.
func NewTracer(opts ...Option) tracer.Tracer {
	return &Tracer{
		Tracer: v2.NewTracer(opts...),
	}
}
