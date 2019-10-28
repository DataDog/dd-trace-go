// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package gqlgen_test

import (
	"log"
	"net/http"

	gqlgentrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/99designs/gqlgen"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/99designs/gqlgen/example/todo"
	"github.com/99designs/gqlgen/handler"
)

func ExampleNew() {
	tracer.Start()
	defer tracer.Stop()

	tracer := gqlgentrace.NewTracer(
		gqlgentrace.WithAnalytics(true),
		gqlgentrace.WithServiceName("todo.server"),
	)
	http.Handle("/query", handler.GraphQL(
		todo.NewExecutableSchema(todo.New()),
		handler.Tracer(tracer),
	))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
