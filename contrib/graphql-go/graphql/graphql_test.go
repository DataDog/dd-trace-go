// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"fmt"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test(t *testing.T) {
	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"hello": {
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (any, error) {
					return "Hello, world!", nil
				},
			},
			"helloNonTrivial": {
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (any, error) {
					return "Hello, world!", nil
				},
			},
		},
	})
	opts := []Option{WithServiceName("test-graphql-service")}
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
		}
		assertSpanMatches(t, spans[0],
			hasNoTag(ext.Error),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[1],
			hasNoTag(ext.Error),
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
			hasNoTag(ext.Error),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasTag(tagGraphqlOperationType, "query"),
			hasOperationName("graphql.resolve"),
			hasTagf(ext.ResourceName, "Query.%s", &foundField),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[3],
			hasTagFrom(tagGraphqlField, &expectedFields, &foundField),
			hasNoTag(ext.Error),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasTag(tagGraphqlOperationType, "query"),
			hasOperationName("graphql.resolve"),
			hasTagf(ext.ResourceName, "Query.%s", &foundField),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[4],
			hasNoTag(ext.Error),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.execute"),
			hasTag(ext.ResourceName, "graphql.execute"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[5],
			hasNoTag(ext.Error),
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
			hasTag(ext.Error, resp.Errors[0].OriginalError()),
			hasTag(tagGraphqlOperationName, "Båd"),
			hasTag(tagGraphqlSource, "query is invalid"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[1],
			hasTag(ext.Error, resp.Errors[0].OriginalError()),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.server"),
			hasTag(ext.ResourceName, "graphql.server"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
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
			hasNoTag(ext.Error),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial, invalidField }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[1],
			hasTag(ext.Error, resp.Errors[0]),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlSource, "query TestQuery { hello, helloNonTrivial, invalidField }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.validate"),
			hasTag(ext.ResourceName, "graphql.validate"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
		assertSpanMatches(t, spans[2],
			hasTag(ext.Error, resp.Errors[0]),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.server"),
			hasTag(ext.ResourceName, "graphql.server"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)
	})
}

type spanMatcher func(*testing.T, mocktracer.Span)

func assertSpanMatches(t *testing.T, span mocktracer.Span, assertions ...spanMatcher) {
	for _, assertion := range assertions {
		assertion(t, span)
	}
}

func hasOperationName(name string) spanMatcher {
	return func(t *testing.T, span mocktracer.Span) {
		_ = assert.Equal(t, name, span.OperationName())
	}
}

func hasTag(name string, value any) spanMatcher {
	return func(t *testing.T, span mocktracer.Span) {
		_ = assert.Equal(t, value, span.Tag(name), "tag %s", name)
	}
}

func hasTagf(name string, pattern string, ptrs ...*string) spanMatcher {
	return func(t *testing.T, span mocktracer.Span) {
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
	return func(t *testing.T, span mocktracer.Span) {
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
	return func(t *testing.T, span mocktracer.Span) {
		_ = assert.Nil(t, span.Tag(name))
	}
}
