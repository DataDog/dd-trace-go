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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
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
	tagGraphqlVariables     = "graphql.variables"
)

// A Tracer implements the graphql-go/trace.Tracer interface by sending traces
// to the Datadog tracer.
type Tracer struct {
	cfg *config
}

var _ tracer.Tracer = (*Tracer)(nil)

// TraceQuery traces a GraphQL query.
func (t *Tracer) TraceQuery(ctx context.Context, queryString, operationName string, variables map[string]interface{}, _ map[string]*introspection.Type) (context.Context, tracer.QueryFinishFunc) {
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.cfg.serviceName),
		ddtracer.Tag(tagGraphqlQuery, queryString),
		ddtracer.Tag(tagGraphqlOperationName, operationName),
		ddtracer.Tag(ext.Component, componentName),
		ddtracer.Measured(),
	}
	if t.cfg.traceVariables {
		for key, value := range variables {
			opts = append(opts, ddtracer.Tag(fmt.Sprintf("%s.%s", tagGraphqlVariables, key), value))
		}
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	span, ctx := ddtracer.StartSpanFromContext(ctx, t.cfg.querySpanName, opts...)

	ctx, request := graphqlsec.StartRequestOperation(ctx, span, types.RequestOperationArgs{
		RawQuery:      queryString,
		OperationName: operationName,
		Variables:     variables,
	})
	ctx, query := graphqlsec.StartExecutionOperation(ctx, span, types.ExecutionOperationArgs{
		Query:         queryString,
		OperationName: operationName,
		Variables:     variables,
	})

	return ctx, func(errs []*errors.QueryError) {
		var err error
		switch n := len(errs); n {
		case 0:
			// err = nil
		case 1:
			err = errs[0]
		default:
			err = fmt.Errorf("%s (and %d more errors)", errs[0], n-1)
		}
		defer span.Finish(ddtracer.WithError(err))
		defer request.Finish(types.RequestOperationRes{Error: err})
		query.Finish(types.ExecutionOperationRes{Error: err})
	}
}

// TraceField traces a GraphQL field access.
func (t *Tracer) TraceField(ctx context.Context, _, typeName, fieldName string, trivial bool, arguments map[string]interface{}) (context.Context, tracer.FieldFinishFunc) {
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
	if t.cfg.traceVariables {
		for key, value := range arguments {
			opts = append(opts, ddtracer.Tag(fmt.Sprintf("%s.%s", tagGraphqlVariables, key), value))
		}
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	span, ctx := ddtracer.StartSpanFromContext(ctx, "graphql.field", opts...)

	ctx, field := graphqlsec.StartResolveOperation(ctx, span, types.ResolveOperationArgs{
		TypeName:  typeName,
		FieldName: fieldName,
		Arguments: arguments,
		Trivial:   trivial,
	})

	return ctx, func(err *errors.QueryError) {
		field.Finish(types.ResolveOperationRes{Error: err})

		// must explicitly check for nil, see issue golang/go#22729
		if err != nil {
			span.Finish(ddtracer.WithError(err))
		} else {
			span.Finish()
		}
	}
}

// NewTracer creates a new Tracer.
func NewTracer(opts ...Option) tracer.Tracer {
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
