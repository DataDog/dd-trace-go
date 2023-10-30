// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

var rootQuery = graphql.NewObject(graphql.ObjectConfig{
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

func Test(t *testing.T) {
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
		require.Len(t, spans, 5)

		traceID := spans[0].TraceID()
		for i := 1; i < len(spans); i++ {
			assert.Equal(t, traceID, spans[i].TraceID())
		}

		assertSpanMatches(t, spans[0],
			hasNoTag(ext.Error),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlQuery, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.parse"),
			hasTag(ext.ResourceName, "graphql.parse"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)

		assertSpanMatches(t, spans[1],
			hasNoTag(ext.Error),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlQuery, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.validation"),
			hasTag(ext.ResourceName, "graphql.validation"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)

		// Fields may be resolved in any order, so span ordering below is not deterministic
		expectedFields := []string{"hello", "helloNonTrivial"}

		assertSpanMatches(t, spans[2],
			hasTagFrom(tagGraphqlField, &expectedFields),
			hasNoTag(ext.Error),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasTag(tagGraphqlType, "query"),
			hasOperationName("graphql.field"),
			hasTag(ext.ResourceName, "graphql.field"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)

		assertSpanMatches(t, spans[3],
			hasTagFrom(tagGraphqlField, &expectedFields),
			hasNoTag(ext.Error),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasTag(tagGraphqlType, "query"),
			hasOperationName("graphql.field"),
			hasTag(ext.ResourceName, "graphql.field"),
			hasTag(ext.Component, "graphql-go/graphql"),
		)

		assertSpanMatches(t, spans[4],
			hasNoTag(ext.Error),
			hasTag(tagGraphqlOperationName, "TestQuery"),
			hasTag(tagGraphqlQuery, "query TestQuery { hello, helloNonTrivial }"),
			hasTag(ext.ServiceName, "test-graphql-service"),
			hasOperationName("graphql.request"),
			hasTag(ext.ResourceName, "graphql.request"),
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
		require.Len(t, spans, 10)

		// First trace
		traceID := spans[0].TraceID()
		for i := 1; i < 5; i++ {
			assert.Equal(t, traceID, spans[i].TraceID())
		}

		// Second trace
		traceID = spans[5].TraceID()
		for i := 6; i < len(spans); i++ {
			assert.Equal(t, traceID, spans[i].TraceID())
		}
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

func hasTag(name string, value string) spanMatcher {
	return func(t *testing.T, span mocktracer.Span) {
		_ = assert.Equal(t, value, span.Tag(name), "tag %s", name)
	}
}

// hasTagFrom asserts the tag with the given name is one of the values in the
// provided slice. If that is the case, the found value is removed from the
// slice.
func hasTagFrom(name string, values *[]string) spanMatcher {
	return func(t *testing.T, span mocktracer.Span) {
		tag := span.Tag(name)
		if assert.Contains(t, *values, tag, "tag %s", name) {
			remaining := make([]string, 0, len(*values)-1)
			for _, value := range *values {
				if value != tag {
					remaining = append(remaining, value)
				}
			}
			*values = remaining
		}
	}
}

func hasNoTag(name string) spanMatcher {
	return func(t *testing.T, span mocktracer.Span) {
		_ = assert.Nil(t, span.Tag(name))
	}
}
