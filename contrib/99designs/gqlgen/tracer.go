// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package gqlgen contains an implementation of a gqlgen tracer, and functions
// to construct and configure the tracer. The tracer can be passed to the gqlgen
// handler (see package github.com/99designs/gqlgen/handler)
//
// Warning: Data obfuscation hasn't been implemented for graphql queries yet,
// any sensitive data in the query will be sent to Datadog as the resource name
// of the span. To ensure no sensitive data is included in your spans, always
// use parameterized graphql queries with sensitive data in variables.
//
// Usage example:
//
//	import (
//		"log"
//		"net/http"
//
//		"github.com/99designs/gqlgen/_examples/todo"
//		"github.com/99designs/gqlgen/graphql/handler"
//
//		"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
//		gqlgentrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/99designs/gqlgen"
//	)
//
//	func Example() {
//		tracer.Start()
//		defer tracer.Stop()
//
//		t := gqlgentrace.NewTracer(
//			gqlgentrace.WithAnalytics(true),
//			gqlgentrace.WithServiceName("todo.server"),
//		)
//		h := handler.NewDefaultServer(todo.NewExecutableSchema(todo.New()))
//		h.Use(t)
//		http.Handle("/query", h)
//		log.Fatal(http.ListenAndServe(":8080", nil))
//	}
package gqlgen

import (
	"context"
	"fmt"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

const componentName = "99designs/gqlgen"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/99designs/gqlgen")
}

const (
	readOp                  = "graphql.read"
	parsingOp               = "graphql.parse"
	validationOp            = "graphql.validate"
	executeOp               = "graphql.execute"
	fieldOp                 = "graphql.field"
	tagGraphqlSource        = "graphql.source"
	tagGraphqlField         = "graphql.field"
	tagGraphqlOperationType = "graphql.operation.type"
	tagGraphqlOperationName = "graphql.operation.name"
)

type gqlTracer struct {
	cfg *config
}

// NewTracer creates a graphql.HandlerExtension instance that can be used with
// a graphql.handler.Server.
// Options can be passed in for further configuration.
func NewTracer(opts ...Option) graphql.HandlerExtension {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &gqlTracer{cfg: cfg}
}

func (t *gqlTracer) ExtensionName() string {
	return "DatadogTracing"
}

func (t *gqlTracer) Validate(_ graphql.ExecutableSchema) error {
	return nil // unimplemented
}

func (t *gqlTracer) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	opCtx := graphql.GetOperationContext(ctx)
	span, ctx := t.createRootSpan(ctx, opCtx)
	ctx, req := graphqlsec.StartRequestOperation(ctx, span, types.RequestOperationArgs{
		RawQuery:      opCtx.RawQuery,
		OperationName: opCtx.OperationName,
		Variables:     opCtx.Variables,
	})
	ctx, query := graphqlsec.StartExecutionOperation(ctx, span, types.ExecutionOperationArgs{
		Query:         opCtx.RawQuery,
		OperationName: opCtx.OperationName,
		Variables:     opCtx.Variables,
	})
	responseHandler := next(ctx)
	return func(ctx context.Context) *graphql.Response {
		response := responseHandler(ctx)
		if span != nil {
			var err error
			if len(response.Errors) > 0 {
				err = response.Errors
			}
			defer span.Finish(tracer.WithError(err))
		}
		query.Finish(types.ExecutionOperationRes{
			Data:  response.Data, // NB - This is raw data, but rather not parse it (possibly expensive).
			Error: response.Errors,
		})
		req.Finish(types.RequestOperationRes{
			Data:  response.Data, // NB - This is raw data, but rather not parse it (possibly expensive).
			Error: response.Errors,
		})
		return response
	}
}

func (t *gqlTracer) InterceptField(ctx context.Context, next graphql.Resolver) (res any, err error) {
	opCtx := graphql.GetOperationContext(ctx)
	if t.cfg.withoutTraceIntrospectionQuery && opCtx.OperationName == "IntrospectionQuery" {
		res, err = next(ctx)
		return
	}

	fieldCtx := graphql.GetFieldContext(ctx)
	isTrivial := !(fieldCtx.IsMethod || fieldCtx.IsResolver)
	if t.cfg.withoutTraceTrivialResolvedFields && isTrivial {
		res, err = next(ctx)
		return
	}

	opts := make([]tracer.StartSpanOption, 0, 6+len(t.cfg.tags))
	for k, v := range t.cfg.tags {
		opts = append(opts, tracer.Tag(k, v))
	}
	opts = append(opts,
		tracer.Tag(tagGraphqlField, fieldCtx.Field.Name),
		tracer.Tag(tagGraphqlOperationType, opCtx.Operation.Operation),
		tracer.Tag(ext.Component, componentName),
		tracer.ResourceName(fmt.Sprintf("%s.%s", fieldCtx.Object, fieldCtx.Field.Name)),
		tracer.Measured(),
	)
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}

	span, ctx := tracer.StartSpanFromContext(ctx, fieldOp, opts...)
	defer func() { span.Finish(tracer.WithError(err)) }()
	ctx, op := graphqlsec.StartResolveOperation(ctx, span, types.ResolveOperationArgs{
		Arguments: fieldCtx.Args,
		TypeName:  fieldCtx.Object,
		FieldName: fieldCtx.Field.Name,
		Trivial:   isTrivial,
	})
	defer func() { op.Finish(types.ResolveOperationRes{Data: res, Error: err}) }()

	res, err = next(ctx)
	return
}

func (*gqlTracer) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	return next(ctx)
}

// createRootSpan creates a graphql server root span starting at the beginning
// of the operation context. If the operation is a subscription, a nil span is
// returned as those may run indefinitely and would be problematic. This function
// also creates child spans (orphans in the case of a subscription) for the
// read, parsing and validation phases of the operation.
func (t *gqlTracer) createRootSpan(ctx context.Context, opCtx *graphql.OperationContext) (ddtrace.Span, context.Context) {
	opts := make([]tracer.StartSpanOption, 0, 7+len(t.cfg.tags))
	for k, v := range t.cfg.tags {
		opts = append(opts, tracer.Tag(k, v))
	}
	opts = append(opts,
		tracer.SpanType(ext.SpanTypeGraphQL),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.ServiceName(t.cfg.serviceName),
		tracer.Tag(ext.Component, componentName),
		tracer.ResourceName(opCtx.RawQuery),
		tracer.StartTime(opCtx.Stats.OperationStart),
	)
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	var rootSpan ddtrace.Span
	if opCtx.Operation.Operation != ast.Subscription {
		// Subscriptions are long running queries which may remain open indefinitely
		// until the subscription ends. We do not create the root span for these.
		rootSpan, ctx = tracer.StartSpanFromContext(ctx, serverSpanName(opCtx), opts...)
	}
	createChildSpan := func(name string, start, finish time.Time) {
		childOpts := []ddtrace.StartSpanOption{
			tracer.StartTime(start),
			tracer.ResourceName(name),
			tracer.Tag(ext.Component, componentName),
		}
		if rootSpan == nil {
			// If there is no root span, decorate the orphan spans with more information
			childOpts = append(childOpts, opts...)
		}
		var childSpan ddtrace.Span
		childSpan, _ = tracer.StartSpanFromContext(ctx, name, childOpts...)
		childSpan.Finish(tracer.FinishTime(finish))
	}
	createChildSpan(readOp, opCtx.Stats.Read.Start, opCtx.Stats.Read.End)
	createChildSpan(parsingOp, opCtx.Stats.Parsing.Start, opCtx.Stats.Parsing.End)
	createChildSpan(validationOp, opCtx.Stats.Validation.Start, opCtx.Stats.Validation.End)
	return rootSpan, ctx
}

func serverSpanName(octx *graphql.OperationContext) string {
	nameV0 := "graphql.request"
	if octx != nil && octx.Operation != nil {
		nameV0 = fmt.Sprintf("%s.%s", ext.SpanTypeGraphQL, octx.Operation.Operation)
	}
	return namingschema.OpNameOverrideV0(namingschema.GraphqlServer, nameV0)
}

// Ensure all of these interfaces are implemented.
var _ interface {
	graphql.HandlerExtension
	graphql.OperationInterceptor
	graphql.FieldInterceptor
	graphql.ResponseInterceptor
} = &gqlTracer{}
