// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package gqlgen contains an implementation of a gqlgen tracer, and functions to construct and configure the tracer.
// The tracer can be passed to the gqlgen handler (see package github.com/99designs/gqlgen/handler)
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
	resolverObject = "resolver.object"
	resolverField  = "resolver.field"
	defaultResourceName = "gqlgen"
)

type gqlTracer struct {
	cfg config
}

// New creates an a graphql.Tracer instance that can be passed to a gqlgen handler.
// Options can be passed in for further configuration.
func New(opts ...Option) graphql.Tracer {
	var t gqlTracer
	defaults(&t.cfg)
	for _, o := range opts {
		o(&t.cfg)
	}
	return &t
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) StartOperationParsing(ctx context.Context) context.Context {
	return ctx
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) EndOperationParsing(ctx context.Context) {
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) StartOperationValidation(ctx context.Context) context.Context {
	return ctx
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) EndOperationValidation(ctx context.Context) {
}

func (t *gqlTracer) StartOperationExecution(ctx context.Context) context.Context {
	rctx := graphql.GetRequestContext(ctx)
	name := defaultResourceName
	if rctx != nil && rctx.OperationName != "" {
		name = rctx.OperationName
	}
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeGraphql),
		tracer.ResourceName(name),
		tracer.ServiceName(t.cfg.serviceName),
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	if s, ok := tracer.SpanFromContext(ctx); ok {
		opts = append(opts, tracer.ChildOf(s.Context()))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, name, opts...)
	return ctx
}

func (t *gqlTracer) StartFieldExecution(ctx context.Context, field graphql.CollectedField) context.Context {
	span, ctx := tracer.StartSpanFromContext(ctx, "Field_"+field.Name)
	span.SetTag("field", field.Name)
	return ctx
}

func (t *gqlTracer) StartFieldResolverExecution(ctx context.Context, rc *graphql.ResolverContext) context.Context {
	// This will be the span created in StartFieldExecution.
	// StartFieldResolverExecution is called only once per StartFieldExecution, so we can add context to the
	// span rather than starting a child span.
	span, _ := tracer.SpanFromContext(ctx)
	span.SetTag(ext.SpanName, rc.Object+"_"+rc.Field.Name)
	span.SetTag(ext.ResourceName, rc.Object+"_"+rc.Field.Name)
	span.SetTag(resolverObject, rc.Object)
	span.SetTag(resolverField, rc.Field.Name)
	return ctx
}

// gqlTracer implements the graphql.Tracer interface.
func (t *gqlTracer) StartFieldChildExecution(ctx context.Context) context.Context {
	return ctx
}

func (t *gqlTracer) EndFieldExecution(ctx context.Context) {
	span, _ := tracer.SpanFromContext(ctx)
	defer span.Finish()
	resCtx := graphql.GetResolverContext(ctx)
	reqCtx := graphql.GetRequestContext(ctx)
	errList := reqCtx.GetErrors(resCtx)
	if len(errList) != 0 {
		span.SetTag(ext.Error, true)
		for idx, err := range errList {
			span.SetTag(fmt.Sprintf("gqlgen.error_%d.message", idx), err.Error())
			span.SetTag(fmt.Sprintf("gqlgen.error_%d.kind", idx), fmt.Sprintf("%T", err))
		}
	}
}

func (t *gqlTracer) EndOperationExecution(ctx context.Context) {
	span, _ := tracer.SpanFromContext(ctx)
	span.Finish()
}
