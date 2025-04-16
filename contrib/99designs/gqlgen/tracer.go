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
	v2 "github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2"

	"github.com/99designs/gqlgen/graphql"
)

// NewTracer creates a graphql.HandlerExtension instance that can be used with
// a graphql.handler.Server.
// Options can be passed in for further configuration.
func NewTracer(opts ...Option) graphql.HandlerExtension {
	return v2.NewTracer(opts...)
}
