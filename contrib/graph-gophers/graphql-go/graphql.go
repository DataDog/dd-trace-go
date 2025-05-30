// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package graphql provides functions to trace the graph-gophers/graphql-go package (https://github.com/graph-gophers/graphql-go).
//
// We use the tracing mechanism available in the
// https://pkg.go.dev/github.com/graph-gophers/graphql-go/trace subpackage.
// Create a new Tracer with `NewTracer` and pass it as an additional option to
// `MustParseSchema`.
package graphql // import "github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2"

import (
	"context"
	"fmt"
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	ddtracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/graphqlsec"
	instrgraphql "github.com/DataDog/dd-trace-go/v2/instrumentation/graphql"

	"github.com/graph-gophers/graphql-go/errors"
	"github.com/graph-gophers/graphql-go/introspection"
	"github.com/graph-gophers/graphql-go/trace/tracer"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGraphGophersGraphQLGo)
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
	opts := []ddtracer.StartSpanOption{
		ddtracer.ServiceName(t.cfg.serviceName),
		ddtracer.Tag(tagGraphqlQuery, queryString),
		ddtracer.Tag(tagGraphqlOperationName, operationName),
		ddtracer.Tag(ext.Component, instrumentation.PackageGraphGophersGraphQLGo),
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

	ctx, request := graphqlsec.StartRequestOperation(ctx, span, graphqlsec.RequestOperationArgs{
		RawQuery:      queryString,
		OperationName: operationName,
		Variables:     variables,
	})
	ctx, query := graphqlsec.StartExecutionOperation(ctx, graphqlsec.ExecutionOperationArgs{
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
		instrgraphql.AddErrorsAsSpanEvents(span, toGraphqlErrors(errs), t.cfg.errExtensions)
		defer span.Finish(ddtracer.WithError(err))
		defer request.Finish(graphqlsec.RequestOperationRes{Error: err})
		query.Finish(graphqlsec.ExecutionOperationRes{Error: err})
	}
}

// TraceField traces a GraphQL field access.
func (t *Tracer) TraceField(ctx context.Context, _, typeName, fieldName string, trivial bool, arguments map[string]interface{}) (context.Context, tracer.FieldFinishFunc) {
	if t.cfg.omitTrivial && trivial {
		return ctx, func(_ *errors.QueryError) {}
	}
	opts := []ddtracer.StartSpanOption{
		ddtracer.ServiceName(t.cfg.serviceName),
		ddtracer.Tag(tagGraphqlField, fieldName),
		ddtracer.Tag(tagGraphqlType, typeName),
		ddtracer.Tag(ext.Component, instrumentation.PackageGraphGophersGraphQLGo),
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

	ctx, field := graphqlsec.StartResolveOperation(ctx, graphqlsec.ResolveOperationArgs{
		TypeName:  typeName,
		FieldName: fieldName,
		Arguments: arguments,
		Trivial:   trivial,
	})

	return ctx, func(err *errors.QueryError) {
		field.Finish(graphqlsec.ResolveOperationRes{Error: err})

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
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/graph-gophers/graphql-go: Configuring Graphql Tracer: %#v", cfg)
	return &Tracer{
		cfg: cfg,
	}
}

func toGraphqlErrors(errs []*errors.QueryError) []instrgraphql.Error {
	res := make([]instrgraphql.Error, 0, len(errs))
	for _, err := range errs {
		locs := make([]instrgraphql.ErrorLocation, 0, len(err.Locations))
		for _, loc := range err.Locations {
			locs = append(locs, instrgraphql.ErrorLocation{
				Line:   loc.Line,
				Column: loc.Column,
			})
		}
		res = append(res, instrgraphql.Error{
			OriginalErr: err,
			Message:     err.Message,
			Locations:   locs,
			Path:        err.Path,
			Extensions:  err.Extensions,
		})
	}
	return res
}
