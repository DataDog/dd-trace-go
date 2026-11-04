// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the gqlgen server constructors from package main and
// runs one query against each server. TestServersInMain builds and runs it.
package main

import (
	"fmt"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/99designs.gqlgen/generated/graph"
)

func main() {
	srv := handler.New(schema())
	srv.AddTransport(transport.POST{})
	query(srv, "NewQuery")

	query(handler.NewDefaultServer(schema()), "DefaultServerQuery")

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func schema() graphql.ExecutableSchema {
	return graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}})
}

// query runs a query named operation, which becomes the resource of the
// graphql.query span.
func query(srv *handler.Server, operation string) {
	var resp map[string]any
	q := fmt.Sprintf(`query %s { topLevel(id: "a") { nested(id: "b") } }`, operation)
	if err := client.New(srv).Post(q, &resp); err != nil {
		panic(err)
	}
}
