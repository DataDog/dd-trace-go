// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql_test

import (
	"log"
	"net/http"

	graphql "github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"

	graphqltrace "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/graph-gophers/graphql-go"
)

type resolver struct{}

func (*resolver) Hello() string { return "Hello, world!" }

func Example() {
	s := `
		schema {
			query: Query
		}
		type Query {
			hello: String!
		}
	`
	schema := graphql.MustParseSchema(s, new(resolver),
		graphql.Tracer(graphqltrace.NewTracer()))
	http.Handle("/query", &relay.Handler{Schema: schema})
	log.Fatal(http.ListenAndServe(":8080", nil))

	// then:
	// $ curl -XPOST -d '{"query": "{ hello }"}' localhost:8080/query
}
