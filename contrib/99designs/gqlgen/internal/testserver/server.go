// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package testserver

//go:generate go run github.com/99designs/gqlgen generate

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2/internal/testserver/graph"
)

func New(t *testing.T, tracer graphql.HandlerExtension) (*handler.Server, *client.Client) {
	t.Helper()

	h := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))

	h.AddTransport(transport.Options{})
	h.AddTransport(transport.GET{})
	h.AddTransport(transport.POST{})

	if tracer != nil {
		h.Use(tracer)
	}

	return h, client.New(h)
}
