// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
	"github.com/stretchr/testify/require"
	graphqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/graph-gophers/graphql-go"
)

type Integration struct {
	server   *httptest.Server
	numSpans int
	opts     []graphqltrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]graphqltrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "graph-gophers/graphql-go"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	schema := graphql.MustParseSchema(testServerSchema, new(testResolver), graphql.Tracer(graphqltrace.NewTracer(i.opts...)))
	i.server = httptest.NewServer(&relay.Handler{Schema: schema})

	t.Cleanup(func() {
		i.numSpans = 0
		i.server.Close()
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	q := `{"query": "query TestQuery() { hello, helloNonTrivial }", "operationName": "TestQuery"}`
	resp, err := http.Post(i.server.URL, "application/json", strings.NewReader(q))
	require.NoError(t, err)
	defer resp.Body.Close()
	i.numSpans += 3
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, graphqltrace.WithServiceName(name))
}

type testResolver struct{}

func (*testResolver) Hello() string                    { return "Hello, world!" }
func (*testResolver) HelloNonTrivial() (string, error) { return "Hello, world!", nil }

const testServerSchema = `
	schema {
		query: Query
	}
	type Query {
		hello: String!
		helloNonTrivial: String!
	}
`
