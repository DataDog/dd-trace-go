// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package gqlgen contains an implementation of a gqlgen tracer, and functions to construct and configure the tracer.
// The tracer can be passed to the gqlgen handler (see package github.com/99designs/gqlgen/handler)
//
// When enabling introspection, please note that introspection queries may cause large traces to be created.
package gqlgen

import (
	"context"
	"fmt"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/99designs/gqlgen/graphql"
)

const (
	tagGraphQLQuery        = "graphql.query"
	tagComplexityLimit     = "complexityLimit"
	tagOperationComplexity = "operationComplexity"
)

const (
	spanGQLGenOperation    = "gqlgen.operation"
	spanGQLGenField        = "gqlgen.field"
)

type gqlTracer struct {
	cfg *config
}

// NewTracer creates an a graphql.Tracer instance that can be passed to a gqlgen handler.
// Options can be passed in for further configuration.
func NewTracer(opts ...Option) graphql.Tracer {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &gqlTracer{cfg: cfg}
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) StartOperationParsing(ctx context.Context) context.Context {
	return ctx
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) EndOperationParsing(ctx context.Context) {
	// not implemented
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) StartOperationValidation(ctx context.Context) context.Context {
	return ctx
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) EndOperationValidation(ctx context.Context) {
	// not implemented
}

func (t *gqlTracer) StartOperationExecution(ctx context.Context) context.Context {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeGraphQL),
		tracer.ServiceName(t.cfg.serviceName),
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	rctx := graphql.GetRequestContext(ctx)
	if rctx != nil {
		opts = append(opts, tracer.Tag(tagGraphQLQuery, rctx.RawQuery))
		if rctx.OperationName != "" {
			opts = append(opts, tracer.ResourceName(rctx.OperationName))
		}
		if rctx.ComplexityLimit > 0 {
			opts = append(opts,
				tracer.Tag(tagComplexityLimit, rctx.ComplexityLimit),
				tracer.Tag(tagOperationComplexity, rctx.OperationComplexity),
			)
		}
		for key, val := range rctx.Variables {
			opts = append(opts,
				tracer.Tag(fmt.Sprintf("variables.%s", key), fmt.Sprintf("%+v", val)),
			)
		}
	}
	if s, ok := tracer.SpanFromContext(ctx); ok {
		opts = append(opts, tracer.ChildOf(s.Context()))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, spanGQLGenOperation, opts...)
	return ctx
}

func (t *gqlTracer) StartFieldExecution(ctx context.Context, field graphql.CollectedField) context.Context {
	span, ctx := tracer.StartSpanFromContext(ctx, spanGQLGenField)
	span.SetTag(ext.ResourceName, field.Name)
	return ctx
}

func (t *gqlTracer) StartFieldResolverExecution(ctx context.Context, rc *graphql.ResolverContext) context.Context {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return ctx
	}
	span.SetTag(ext.ResourceName, rc.Object+"."+rc.Field.Name)
	return ctx
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) StartFieldChildExecution(ctx context.Context) context.Context {
	return ctx
}

func (t *gqlTracer) EndFieldExecution(ctx context.Context) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	defer span.Finish()
	resCtx := graphql.GetResolverContext(ctx)
	if resCtx == nil {
		return
	}
	reqCtx := graphql.GetRequestContext(ctx)
	if reqCtx == nil {
		return
	}
	errs := reqCtx.GetErrors(resCtx)
	switch n := len(errs); n {
	case 0:
		// no error
	case 1:
		span.SetTag(ext.Error, errs[0])
	default:
		span.SetTag(ext.Error, fmt.Sprintf("%s (and %d more errors)", errs[0], n-1))
	}
}

func (t *gqlTracer) EndOperationExecution(ctx context.Context) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	span.Finish()
}
