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
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	defaultGraphqlOperation = "graphql.request"

	readOp       = "graphql.read"
	parsingOp    = "graphql.parse"
	validationOp = "graphql.validate"
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

func (t *gqlTracer) Validate(schema graphql.ExecutableSchema) error {
	return nil // unimplemented
}

func (t *gqlTracer) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeGraphQL),
		tracer.ServiceName(t.cfg.serviceName),
		tracer.Tag(ext.Component, "99designs/gqlgen"),
	}
	if !math.IsNaN(t.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, t.cfg.analyticsRate))
	}
	var (
		octx *graphql.OperationContext
	)
	name := defaultGraphqlOperation
	if graphql.HasOperationContext(ctx) {
		// Variables in the operation will be left out of the tags
		// until obfuscation is implemented in the agent.
		octx = graphql.GetOperationContext(ctx)
		if octx.Operation != nil {
			if octx.Operation.Operation == ast.Subscription {
				// These are long running queries for a subscription,
				// remaining open indefinitely until a subscription ends.
				// Return early and do not create these spans.
				return next(ctx)
			}
			name = fmt.Sprintf("%s.%s", ext.SpanTypeGraphQL, octx.Operation.Operation)
		}
		if octx.RawQuery != "" {
			opts = append(opts, tracer.ResourceName(octx.RawQuery))
		}
		opts = append(opts, tracer.StartTime(octx.Stats.OperationStart))
	}
	var span ddtrace.SpanW3C
	span, ctx = tracer.StartSpanFromContext(ctx, name, opts...)
	defer func() {
		var errs []string
		for _, err := range graphql.GetErrors(ctx) {
			errs = append(errs, err.Message)
		}
		var err error
		if len(errs) > 0 {
			err = fmt.Errorf(strings.Join(errs, ", "))
		}
		span.Finish(tracer.WithError(err))
	}()

	if octx != nil {
		// Create child spans based on the stats in the operation context.
		createChildSpan := func(name string, start, finish time.Time) {
			var childOpts []ddtrace.StartSpanOption
			childOpts = append(childOpts, tracer.StartTime(start))
			childOpts = append(childOpts, tracer.ResourceName(name))
			childOpts = append(childOpts, tracer.Tag(ext.Component, "99designs/gqlgen"))
			var childSpan ddtrace.SpanW3C
			childSpan, _ = tracer.StartSpanFromContext(ctx, name, childOpts...)
			childSpan.Finish(tracer.FinishTime(finish))
		}
		createChildSpan(readOp, octx.Stats.Read.Start, octx.Stats.Read.End)
		createChildSpan(parsingOp, octx.Stats.Parsing.Start, octx.Stats.Parsing.End)
		createChildSpan(validationOp, octx.Stats.Validation.Start, octx.Stats.Validation.End)
	}
	return next(ctx)
}

// Ensure all of these interfaces are implemented.
var _ interface {
	graphql.HandlerExtension
	graphql.ResponseInterceptor
} = &gqlTracer{}
