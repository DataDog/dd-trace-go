// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import (
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestOtelcWrapSchema(t *testing.T) {
	newConfig := func() graphql.SchemaConfig {
		return graphql.SchemaConfig{
			Query: graphql.NewObject(graphql.ObjectConfig{
				Name: "Query",
				Fields: graphql.Fields{
					"hello": {
						Type: graphql.String,
						Resolve: func(graphql.ResolveParams) (any, error) {
							return "Hello, world!", nil
						},
					},
				},
			}),
		}
	}
	spanNames := func(t *testing.T, schema graphql.Schema) []string {
		t.Helper()
		mt := mocktracer.Start()
		defer mt.Stop()
		res := graphql.Do(graphql.Params{Schema: schema, RequestString: "{ hello }"})
		require.Empty(t, res.Errors)
		spans := mt.FinishedSpans()
		names := make([]string, 0, len(spans))
		for _, s := range spans {
			names = append(names, s.OperationName())
		}
		return names
	}

	t.Run("same spans as NewSchema", func(t *testing.T) {
		want, err := NewSchema(newConfig())
		require.NoError(t, err)
		got, err := otelcWrapSchema(graphql.NewSchema(newConfig()))
		require.NoError(t, err)
		assert.ElementsMatch(t, spanNames(t, want), spanNames(t, got))
		assert.Len(t, spanNames(t, got), 5)
	})

	t.Run("error", func(t *testing.T) {
		in, inErr := graphql.NewSchema(graphql.SchemaConfig{})
		require.Error(t, inErr)
		out, err := otelcWrapSchema(in, inErr)
		assert.Equal(t, inErr, err)
		assert.Equal(t, in, out)
	})
}
