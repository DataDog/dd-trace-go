// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package gqlgen_test

import (
	"log"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/99designs/gqlgen"

	"github.com/99designs/gqlgen/example/todo"
	"github.com/99designs/gqlgen/handler"
)

func ExampleNew() {
	tracer := gqlgen.New(
		gqlgen.WithAnalytics(true),
		gqlgen.WithServiceName("todoServer"),
	)
	http.Handle("/query", handler.GraphQL(
		todo.NewExecutableSchema(todo.New()),
		// We can add our tracer in when we set up the query handler.
		handler.Tracer(tracer),
	))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
