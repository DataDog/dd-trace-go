// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen_test

import (
	"log"
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler/testserver"

	gqlgentrace "github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	t := gqlgentrace.NewTracer(
		gqlgentrace.WithAnalytics(true),
		gqlgentrace.WithService("todo.server"),
	)
	h := testserver.New() // replace with your own actual server
	h.Use(t)
	http.Handle("/query", h)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
