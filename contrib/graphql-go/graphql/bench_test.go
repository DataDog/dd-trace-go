// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
)

func BenchmarkGraphQL(b *testing.B) {
	type objectType struct {
		id string
	}
	topLevelType := graphql.NewObject(graphql.ObjectConfig{
		Name: "TopLevel",
		Fields: graphql.Fields{
			"nested": &graphql.Field{
				Type: graphql.NewNonNull(graphql.String),
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					obj := p.Source.(*objectType)
					id := p.Args["id"].(string)
					return fmt.Sprintf("%s/%s", obj.id, id), nil
				},
			},
		},
	})
	mapInput := graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "MapInput",
		Fields: graphql.InputObjectConfigFieldMap{
			"ids":   &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(graphql.String)))},
			"bool":  &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Boolean)},
			"float": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Float)},
		},
	})
	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"topLevel": {
				Type: topLevelType,
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					id := p.Args["id"].(string)
					return &objectType{id}, nil
				},
			},
			"topLevelMapped": {
				Type: topLevelType,
				Args: graphql.FieldConfigArgument{
					"map":   &graphql.ArgumentConfig{Type: graphql.NewNonNull(mapInput)},
					"key":   &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
					"index": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.Int)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					obj := p.Args["map"].(map[string]any)
					key := p.Args["key"].(string)
					ids := obj[key].([]any)
					index := p.Args["index"].(int)
					id := ids[index].(string)
					return &objectType{id}, nil
				},
			},
		},
	})
	defer enableAppSecBench(b)()
	const (
		topLevelAttack = "he protec"
		nestedAttack   = "he attac, but most importantly: he Tupac"
	)
	testCases := map[string]struct {
		query     string
		variables map[string]any
		events    map[string]string
	}{
		"basic": {
			query: `query TestQuery($topLevelId: String!, $nestedId: String!) { topLevel(id: $topLevelId) { nested(id: $nestedId) } }`,
			variables: map[string]any{
				"topLevelId": topLevelAttack,
				"nestedId":   nestedAttack,
			},
			events: map[string]string{
				"test-rule-001": "graphql.resolve(topLevel)",
				"test-rule-002": "graphql.resolve(nested)",
			},
		},
		"with-default-parameter": {
			query: fmt.Sprintf(`query TestQuery($topLevelId: String = %#v, $nestedId: String!) { topLevel(id: $topLevelId) { nested(id: $nestedId) } }`, topLevelAttack),
			variables: map[string]any{
				// "topLevelId" omitted (default value used)
				"nestedId": nestedAttack,
			},
			events: map[string]string{
				"test-rule-001": "graphql.resolve(topLevel)",
				"test-rule-002": "graphql.resolve(nested)",
			},
		},
		"embedded-variable": {
			query: `query TestQuery($topLevelId: String!, $nestedId: String!) {
				topLevel: topLevelMapped(map: { ids: ["foo", $topLevelId, "baz"], bool: true, float: 3.14 }, key: "ids", index: 1) {
					nested(id: $nestedId)
				}
			}`,
			variables: map[string]any{
				"topLevelId": topLevelAttack,
				"nestedId":   nestedAttack,
			},
			events: map[string]string{
				"test-rule-001": "graphql.resolve(topLevelMapped)",
				"test-rule-002": "graphql.resolve(nested)",
			},
		},
	}

	b.Run("version=baseline", func(b *testing.B) {
		for name, tc := range testCases {
			b.Run(fmt.Sprintf("scenario=%s", name), func(b *testing.B) {
				b.StopTimer()
				b.ReportAllocs()
				schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: rootQuery})
				require.NoError(b, err)
				for i := 0; i < b.N; i++ {
					b.StartTimer()
					resp := graphql.Do(graphql.Params{
						Schema:         schema,
						RequestString:  tc.query,
						OperationName:  "TestQuery",
						VariableValues: tc.variables,
					})
					b.StopTimer()
					require.Empty(b, resp.Errors)
				}
			})
		}
	})

	b.Run("version=dyngo", func(b *testing.B) {
		for name, tc := range testCases {
			b.Run(fmt.Sprintf("scenario=%s", name), func(b *testing.B) {
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
						Schema:         schema,
						RequestString:  tc.query,
						OperationName:  "TestQuery",
						VariableValues: tc.variables,
					})
					b.StopTimer()
					require.Empty(b, resp.Errors)
					spans := mt.FinishedSpans()
					require.Len(b, spans, 6)
					mt.Reset()
				}
			})
		}
	})
}

// enableAppSec ensures the environment variable to enable appsec is active, and
// returns a function to restore the previous environment state.
func enableAppSecBench(b *testing.B) func() {
	const rules = `{
		"version": "2.2",
		"metadata": {
			"rules_version": "0.1337.42"
		},
		"rules": [
			{
				"id": "test-rule-001",
				"name": "Phony rule number 1",
				"tags": {
					"category": "canary",
					"type": "meme-protec"
				},
				"conditions": [{
					"operator": "phrase_match",
					"parameters": {
						"inputs": [{ "address": "graphql.server.resolver" }],
						"list": ["he protec"]
					}
				}],
				"transformers": ["lowercase"],
				"on_match": []
			},
			{
				"id": "test-rule-002",
				"name": "Phony rule number 2",
				"tags": {
					"category": "canary",
					"type": "meme-attac"
				},
				"conditions": [{
					"operator": "phrase_match",
					"parameters": {
						"inputs": [{ "address": "graphql.server.resolver" }],
						"list": ["he attac"]
					}
				}],
				"transformers": ["lowercase"],
				"on_match": []
			},
			{
				"id": "test-rule-003",
				"name": "Phony rule number 3",
				"tags": {
					"category": "canary",
					"type": "meme-tupac"
				},
				"conditions": [{
					"operator": "phrase_match",
					"parameters": {
						"inputs": [{ "address": "graphql.server.all_resolvers" }],
						"list": ["he tupac"]
					}
				}],
				"transformers": ["lowercase"],
				"on_match": []
			}
		]
	}`
	tmpDir, err := os.MkdirTemp("", "dd-trace-go.graphql-go.graphql.appsec_test.rules-*")
	require.NoError(b, err)
	rulesFile := path.Join(tmpDir, "rules.json")
	err = os.WriteFile(rulesFile, []byte(rules), 0644)
	require.NoError(b, err)
	b.Setenv("DD_APPSEC_ENABLED", "1")
	b.Setenv("DD_APPSEC_RULES", rulesFile)
	appsec.Start()
	restore := func() {
		appsec.Stop()
		_ = os.RemoveAll(tmpDir)
	}
	if !appsec.Enabled() {
		restore()
		b.Skip("could not enable appsec: this platform is likely not supported")
	}
	return restore
}
