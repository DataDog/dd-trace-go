// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package gqlgen provides functions to trace the 99designs/gqlgen package (https://github.com/99designs/gqlgen).
package gqlgen_test

import (
	"log"
	"net/http"

	"github.com/99designs/gqlgen/_examples/todo"
	"github.com/99designs/gqlgen/graphql/handler"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	gqlgentrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/99designs/gqlgen"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	t := gqlgentrace.NewTracer(
		gqlgentrace.WithAnalytics(true),
		gqlgentrace.WithServiceName("todo.server"),
	)
	h := handler.NewDefaultServer(todo.NewExecutableSchema(todo.New()))
	h.Use(t)
	http.Handle("/query", h)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
