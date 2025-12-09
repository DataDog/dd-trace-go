// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test(t *testing.T) {
	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"hello": {
				Type: graphql.String,
				Resolve: func(_ graphql.ResolveParams) (any, error) {
					return "Hello, world!", nil
				},
			},
			"helloNonTrivial": {
				Type: graphql.String,
				Resolve: func(_ graphql.ResolveParams) (any, error) {
					return "Hello, world!", nil
				},
			},
		},
	})
	opts := []Option{WithService("test-graphql-service")}
	schema, err := NewSchema(
		graphql.SchemaConfig{
			Query: rootQuery,
		}, opts...,
	)
	require.NoError(t, err)

	t.Run("single", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		resp := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: `query TestQuery { hello, helloNonTrivial }`,
			OperationName: "TestQuery",
		})
		require.Empty(t, resp.Errors)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 6)
		traceID := spans[0].TraceID()
		for i := 1; i < len(spans); i++ {
			assert.Equal(t, traceID, spans[i].TraceID())
			assert.Equal(t, string(instrumentation.PackageGraphQLGoGraphQL), spans[i].Integration())
		}
		assertSpanMatches(t, spans[0],
			hasNoTag(ext.ErrorMsg),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[1],
			hasNoTag(ext.ErrorMsg),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.validate"),
			hasTag(ext.ResourceName, "graphql.validate"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		// Fields may be resolved in any order, so span ordering below is not deterministic
		expectedFields := []string{"hello", "helloNonTrivial"}
		var foundField string
		assertSpanMatches(t, spans[2],
			hasTagFrom(tagGraphqlField, &expectedFields, &foundField),
			hasNoTag(ext.ErrorMsg),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasTag(tagGraphqlOperationType, "query"),
			hasOperationName("graphql.resolve"),
			hasTagf(ext.ResourceName, "Query.%s", &foundField),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[3],
			hasTagFrom(tagGraphqlField, &expectedFields, &foundField),
			hasNoTag(ext.ErrorMsg),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasTag(tagGraphqlOperationType, "query"),
			hasOperationName("graphql.resolve"),
			hasTagf(ext.ResourceName, "Query.%s", &foundField),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[4],
			hasNoTag(ext.ErrorMsg),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.execute"),
			hasTag(ext.ResourceName, "graphql.execute"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[5],
			hasNoTag(ext.ErrorMsg),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.server"),
			hasTag(ext.ResourceName, "graphql.server"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
	})

	t.Run("multiple", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		for i := 0; i < 2; i++ {
			resp := graphql.Do(graphql.Params{
				Schema:        schema,
				RequestString: `query TestQuery { hello, helloNonTrivial }`,
				OperationName: "TestQuery",
			})
			require.Empty(t, resp.Errors)
		}
		spans := mt.FinishedSpans()
		require.Len(t, spans, 12)
		// First trace
		traceID := spans[0].TraceID()
		for i := 1; i < 6; i++ {
			assert.Equal(t, traceID, spans[i].TraceID())
		}
		// Second trace
		traceID = spans[6].TraceID()
		for i := 7; i < len(spans); i++ {
			assert.Equal(t, traceID, spans[i].TraceID())
		}
	})

	t.Run("request fails parsing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		resp := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: `query is invalid`,
			OperationName: "Båd",
		})
		// There is a single error (the query is invalid)
		require.Len(t, resp.Errors, 1)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		assertSpanMatches(t, spans[0],
			hasTag(ext.ErrorMsg, resp.Errors[0].OriginalError().Error()),
			hasTag(tagGraphqlOperationName, "Båd"),
			hasTag(tagGraphqlSource, "query is invalid"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assert.Equal(t, string(instrumentation.PackageGraphQLGoGraphQL), spans[0].Integration())
		assertSpanMatches(t, spans[1],
			hasTag(ext.ErrorMsg, resp.Errors[0].OriginalError().Error()),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.server"),
			hasTag(ext.ResourceName, "graphql.server"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assert.Equal(t, string(instrumentation.PackageGraphQLGoGraphQL), spans[1].Integration())
	})

	t.Run("request fails validation", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		resp := graphql.Do(graphql.Params{
			Schema:        schema,
			RequestString: `query TestQuery { hello, helloNonTrivial, invalidField }`,
			OperationName: "TestQuery",
		})
		// There is a single error (the query is invalid)
		require.Len(t, resp.Errors, 1)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 3)
		assertSpanMatches(t, spans[0],
			hasNoTag(ext.ErrorMsg),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial, invalidField }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assert.Equal(t, string(instrumentation.PackageGraphQLGoGraphQL), spans[0].Integration())
		assertSpanMatches(t, spans[1],
			hasTag(ext.ErrorMsg, resp.Errors[0].Error()),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial, invalidField }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.validate"),
			hasTag(ext.ResourceName, "graphql.validate"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assert.Equal(t, string(instrumentation.PackageGraphQLGoGraphQL), spans[1].Integration())
		assertSpanMatches(t, spans[2],
			hasTag(ext.ErrorMsg, resp.Errors[0].Error()),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.server"),
			hasTag(ext.ResourceName, "graphql.server"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assert.Equal(t, string(instrumentation.PackageGraphQLGoGraphQL), spans[2].Integration())
	})
}

func TestErrorsAsSpanEvents(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, WithErrorExtensions("str", "float", "int", "bool", "slice", "unsupported_type_stringified"))
	defer srv.Close()

	q := `{"query": "{ withError }"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(q))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 5)

	s0 := spans[3]
	assert.Equal(t, "graphql.execute", s0.OperationName())
	assert.NotNil(t, s0.Tag(ext.ErrorMsg))

	events := s0.Events()
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "dd.graphql.query.error", evt.Name)
	assert.NotEmpty(t, evt.TimeUnixNano)
	assert.NotEmpty(t, evt.Attributes["stacktrace"])
	evt.AssertAttributes(t, map[string]any{
		"message":          "test error",
		"path":             []string{"withError"},
		"locations":        []string{"1:3"},
		"stacktrace":       evt.Attributes["stacktrace"],
		"type":             "gqlerrors.FormattedError",
		"extensions.str":   "1",
		"extensions.int":   1,
		"extensions.float": 1.1,
		"extensions.bool":  true,
		"extensions.slice": []string{"1", "2"},
		"extensions.unsupported_type_stringified": "[1,\"foo\"]",
	})

	// the rest of the spans should not have span events
	for _, s := range spans {
		if s.OperationName() == "graphql.execute" {
			continue
		}
		assert.Emptyf(t, s.Events(), "span %s should not have span events", s.OperationName())
	}
}

func newTestServer(t *testing.T, opts ...Option) *httptest.Server {
	cfg := graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"withError": &graphql.Field{
					Args: graphql.FieldConfigArgument{},
					Type: graphql.ID,
					Resolve: func(_ graphql.ResolveParams) (interface{}, error) {
						return nil, customError{
							message: "test error",
							extensions: map[string]any{
								"int":                          1,
								"float":                        1.1,
								"str":                          "1",
								"bool":                         true,
								"slice":                        []string{"1", "2"},
								"unsupported_type_stringified": []any{1, "foo"},
								"not_captured":                 "nope",
							},
						}
					},
				},
			},
		}),
	}
	schema, err := NewSchema(cfg, opts...)
	require.NoError(t, err)

	h := handler.New(&handler.Config{Schema: &schema, Pretty: true, GraphiQL: true})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

type customError struct {
	message    string
	extensions map[string]any
}

func (e customError) Error() string {
	return e.message
}

func (e customError) Extensions() map[string]any {
	return e.extensions
}

type spanMatcher func(*testing.T, *mocktracer.Span)

func assertSpanMatches(t *testing.T, span *mocktracer.Span, assertions ...spanMatcher) {
	for _, assertion := range assertions {
		assertion(t, span)
	}
}

func hasOperationName(name string) spanMatcher {
	return func(t *testing.T, span *mocktracer.Span) {
		_ = assert.Equal(t, name, span.OperationName())
	}
}

func hasTag(name string, value any) spanMatcher {
	return func(t *testing.T, span *mocktracer.Span) {
		_ = assert.Equal(t, value, span.Tag(name), "tag %s", name)
	}
}

func hasTagf(name string, pattern string, ptrs ...*string) spanMatcher {
	return func(t *testing.T, span *mocktracer.Span) {
		args := make([]any, len(ptrs))
		for i, ptr := range ptrs {
			if ptr != nil {
				args[i] = *ptr
			}
		}
		expected := fmt.Sprintf(pattern, args...)
		_ = assert.Equal(t, expected, span.Tag(name), "tag %s", name)
	}
}

// hasTagFrom asserts the tag with the given name is one of the values in the
// provided slice. If that is the case, the found value is removed from the
// slice. If found is non-nil, it's value will be set to the found value.
func hasTagFrom[T comparable](name string, values *[]T, found *T) spanMatcher {
	return func(t *testing.T, span *mocktracer.Span) {
		tag, _ := span.Tag(name).(T)
		if assert.Contains(t, *values, tag, "tag %s", name) {
			remaining := make([]T, 0, len(*values)-1)
			for _, value := range *values {
				if value != tag {
					remaining = append(remaining, value)
				}
			}
			*values = remaining
			if found != nil {
				*found = tag
			}
		}
	}
}

func hasNoTag(name string) spanMatcher {
	return func(t *testing.T, span *mocktracer.Span) {
		_ = assert.Nil(t, span.Tag(name))
	}
}
