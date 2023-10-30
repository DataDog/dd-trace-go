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
	"fmt"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/graph-gophers/graphql-go/errors"
	"github.com/graph-gophers/graphql-go/introspection"
	"github.com/graph-gophers/graphql-go/trace/tracer"
)

const componentName = "graph-gophers/graphql-go"

func init() {
	telemetry.LoadIntegration(componentName)
	ddtracer.MarkIntegrationImported("github.com/graph-gophers/graphql-go")
}

const (
	tagGraphqlField         = "graphql.field"
	tagGraphqlQuery         = "graphql.query"
	tagGraphqlType          = "graphql.type"
	tagGraphqlOperationName = "graphql.operation.name"
)

// A Tracer implements the graphql-go/trace.Tracer and graphql-go/trace.ValidationTracer interface
// by sending traces to the Datadog tracer.
type Tracer struct {
	cfg *config
}

// TraceQuery traces a GraphQL query.
func (t *Tracer) TraceQuery(ctx context.Context, queryString string, operationName string, variables map[string]interface{}, _ map[string]*introspection.Type) (context.Context, tracer.QueryFinishFunc) {
	ctx, op := graphqlsec.StartQuery(ctx, graphqlsec.QueryArguments{
		Query:         queryString,
		OperationName: operationName,
		Variables:     variables,
	})

	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.cfg.serviceName),
		ddtracer.Tag(tagGraphqlQuery, queryString),
		ddtracer.Tag(tagGraphqlOperationName, operationName),
		ddtracer.Tag(ext.Component, componentName),
		ddtracer.Measured(),
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	span, ctx := ddtracer.StartSpanFromContext(ctx, t.cfg.querySpanName, opts...)

	return ctx, func(errs []*errors.QueryError) {
		err := toError(errs)
		defer op.Finish(graphqlsec.QueryResult{Error: err})
		span.Finish(ddtracer.WithError(err))
	}
}

// TraceField traces a GraphQL field access.
func (t *Tracer) TraceField(ctx context.Context, _ string, typeName string, fieldName string, trivial bool, variables map[string]interface{}) (context.Context, tracer.FieldFinishFunc) {
	ctx, op := graphqlsec.StartField(ctx, graphqlsec.FieldArguments{
		Arguments: variables,
		FieldName: fieldName,
		Trivial:   trivial,
		TypeName:  typeName,
	})

	if t.cfg.omitTrivial && trivial {
		return ctx, func(queryError *errors.QueryError) {}
	}
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.cfg.serviceName),
		ddtracer.Tag(tagGraphqlField, fieldName),
		ddtracer.Tag(tagGraphqlType, typeName),
		ddtracer.Tag(ext.Component, componentName),
		ddtracer.Measured(),
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	span, ctx := ddtracer.StartSpanFromContext(ctx, "graphql.field", opts...)

	return ctx, func(err *errors.QueryError) {
		defer op.Finish(graphqlsec.FieldResult{Error: err})
		// must explicitly check for nil, see issue golang/go#22729
		if err != nil {
			span.Finish(ddtracer.WithError(err))
		} else {
			span.Finish()
		}
	}
}

// TraceValidation traces GraphQL input validation against the schema.
func (t *Tracer) TraceValidation(ctx context.Context) func([]*errors.QueryError) {
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.cfg.serviceName),
		ddtracer.Tag(ext.Component, componentName),
		ddtracer.Measured(),
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	span, _ := ddtracer.StartSpanFromContext(ctx, "graphql.validation", opts...)

	return func(errs []*errors.QueryError) {
		span.Finish(ddtracer.WithError(toError(errs)))
	}
}

type CompleteTracer interface {
	tracer.Tracer
	tracer.ValidationTracer
}

// NewTracer creates a new Tracer.
func NewTracer(opts ...Option) CompleteTracer {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/graph-gophers/graphql-go: Configuring Graphql Tracer: %#v", cfg)
	return &Tracer{
		cfg: cfg,
	}
}

func toError(errs []*errors.QueryError) error {
	switch n := len(errs); n {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return fmt.Errorf("%w (and %d more errors)", errs[0], n-1)
	}
}
