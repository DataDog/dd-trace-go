// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
)

func TestAppSec(t *testing.T) {
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
	opts := []Option{WithServiceName("test-graphql-service")}
	schema, err := NewSchema(graphql.SchemaConfig{Query: rootQuery}, opts...)
	require.NoError(t, err)
	restore := enableAppSec(t)
	defer restore()

	t.Run("monitoring", func(t *testing.T) {
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

		for name, tc := range testCases {
			t.Run(name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				resp := graphql.Do(graphql.Params{
					Schema:         schema,
					RequestString:  tc.query,
					OperationName:  "TestQuery",
					VariableValues: tc.variables,
					Context:        context.Background(),
				})
				// Ensure the query produced the expected results...
				require.Empty(t, resp.Errors)
				require.Equal(t, map[string]any{"topLevel": map[string]any{"nested": fmt.Sprintf("%s/%s", topLevelAttack, nestedAttack)}}, resp.Data)
				// Ensure the query produced the expected appsec events
				spans := mt.FinishedSpans()
				require.NotEmpty(t, spans)
				// The last finished span (which is GraphQL entry) should have the "_dd.appsec.enabled" tag.
				require.Equal(t, 1, spans[len(spans)-1].Tag("_dd.appsec.enabled"))
				events := make(map[string]string)
				type ddAppsecJSON struct {
					Triggers []struct {
						Rule struct {
							ID string `json:"id"`
						} `json:"rule"`
					} `json:"triggers"`
				}
				// Search for AppSec events in the set of spans
				for _, span := range spans {
					jsonText, ok := span.Tag("_dd.appsec.json").(string)
					if !ok || jsonText == "" {
						continue
					}
					var parsed ddAppsecJSON
					err := json.Unmarshal([]byte(jsonText), &parsed)
					require.NoError(t, err)

					require.Len(t, parsed.Triggers, 1, "expected exactly 1 trigger on %s span", span.OperationName())
					ruleID := parsed.Triggers[0].Rule.ID
					_, duplicate := events[ruleID]
					require.False(t, duplicate, "found duplicated hit for rule %s", ruleID)
					var origin string
					switch name := span.OperationName(); name {
					case spanResolve:
						field := span.Tag(tagGraphqlField).(string)
						origin = fmt.Sprintf("%s(%s)", spanResolve, field)
					case spanExecute:
						origin = spanExecute
					default:
						require.Fail(t, "rule trigger recorded on unecpected span", "rule %s recorded a hit on unexpected span %s", ruleID, name)
					}
					events[ruleID] = origin
				}
				// Ensure they match the expected outcome
				require.Equal(t, tc.events, events)
			})
		}
	})
}

// enableAppSec ensures the environment variable to enable appsec is active, and
// returns a function to restore the previous environment state.
func enableAppSec(t *testing.T) func() {
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
	require.NoError(t, err)
	rulesFile := path.Join(tmpDir, "rules.json")
	err = os.WriteFile(rulesFile, []byte(rules), 0644)
	require.NoError(t, err)
	restoreDdAppsecEnabled := setEnv("DD_APPSEC_ENABLED", "1")
	restoreDdAppsecRules := setEnv("DD_APPSEC_RULES", rulesFile)
	appsec.Start()
	restore := func() {
		appsec.Stop()
		restoreDdAppsecEnabled()
		restoreDdAppsecRules()
		_ = os.RemoveAll(tmpDir)
	}
	if !appsec.Enabled() {
		restore()
		t.Skip("could not enable appsec: this platform is likely not supported")
	}
	return restore
}

// setEnv sets an the environment variable named `name` to `value` and returns
// a function that restores the variable to it's original value.
func setEnv(name string, value string) func() {
	oldVal := os.Getenv(name)
	os.Setenv(name, value)
	return func() {
		os.Setenv(name, oldVal)
	}
}
