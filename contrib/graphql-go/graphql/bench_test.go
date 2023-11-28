// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func Benchmark(b *testing.B) {
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

	b.Run("baseline", func(b *testing.B) {
		b.StopTimer()
		b.ReportAllocs()

		schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: rootQuery})
		require.NoError(b, err)

		for i := 0; i < b.N; i++ {
			b.StartTimer()
			resp := graphql.Do(graphql.Params{
				Schema:        schema,
				RequestString: `query TestQuery { hello, helloNonTrivial }`,
				OperationName: "TestQuery",
			})
			b.StopTimer()
			require.Empty(b, resp.Errors)
		}
	})

	b.Run("instrumented", func(b *testing.B) {
		b.StopTimer()
		b.ReportAllocs()

		opts := []Option{WithServiceName("test-graphql-service")}
		schema, err := NewSchema(
			graphql.SchemaConfig{
				Query: rootQuery,
			}, opts...,
		)
		require.NoError(b, err)

		mt := mocktracer.Start()
		defer mt.Stop()

		for i := 0; i < b.N; i++ {
			b.StartTimer()
			resp := graphql.Do(graphql.Params{
				Schema:        schema,
				RequestString: `query TestQuery { hello, helloNonTrivial }`,
				OperationName: "TestQuery",
			})
			b.StopTimer()
			require.Empty(b, resp.Errors)

			spans := mt.FinishedSpans()
			require.Len(b, spans, 6)
			mt.Reset()
		}
	})
}
