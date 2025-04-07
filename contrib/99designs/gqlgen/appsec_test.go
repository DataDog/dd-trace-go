// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestAppSec(t *testing.T) {
	restore := enableAppSec(t)
	defer restore()

	t.Run("monitoring", func(t *testing.T) {
		const (
			topLevelAttack = "he protec"
			nestedAttack   = "he attac, but most importantly: he Tupac"
		)
		schema := gqlparser.MustLoadSchema(&ast.Source{Input: `type Query {
			topLevel(id: String!): TopLevel!
			topLevelMapped(map: MapInput!, key: String!, index: Int!): TopLevel!
		}

		type TopLevel {
			nested(id: String!): String!
		}

		input MapInput {
			ids: [String!]!
			bool: Boolean!
			float: Float!
		}`})
		server := handler.New(&graphql.ExecutableSchemaMock{
			ExecFunc:   execFunc,
			SchemaFunc: func() *ast.Schema { return schema },
		})
		server.Use(NewTracer())
		server.AddTransport(transport.POST{})
		c := client.New(server)
		testCases := map[string]struct {
			query     string
			variables map[string]any
		}{
			"basic": {
				query: `query TestQuery($topLevelId: String!, $nestedId: String!) { topLevel(id: $topLevelId) { nested(id: $nestedId) } }`,
				variables: map[string]any{
					"topLevelId": topLevelAttack,
					"nestedId":   nestedAttack,
				},
			},
			"with-default-parameter": {
				query: fmt.Sprintf(`query TestQuery($topLevelId: String = %#v, $nestedId: String!) { topLevel(id: $topLevelId) { nested(id: $nestedId) } }`, topLevelAttack),
				variables: map[string]any{
					// "topLevelId" omitted (default value used)
					"nestedId": nestedAttack,
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
			},
		}
		for name, tc := range testCases {
			t.Run(name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				var resp map[string]any
				err := c.Post(
					tc.query, &resp,
					client.Var("topLevelId", topLevelAttack), client.Var("nestedId", nestedAttack),
					client.Operation("TestQuery"),
				)
				require.NoError(t, err)

				require.Equal(t, map[string]any{"topLevel": map[string]any{"nested": fmt.Sprintf("%s/%s", topLevelAttack, nestedAttack)}}, resp)

				// Ensure the query produced the expected appsec events
				spans := mt.FinishedSpans()
				require.NotEmpty(t, spans)

				// The last finished span (which is GraphQL entry) should have the "_dd.appsec.enabled" tag.
				span := spans[len(spans)-1]
				require.Equal(t, float64(1), span.Tag("_dd.appsec.enabled"))

				type ddAppsecJSON struct {
					Triggers []struct {
						Rule struct {
							ID string `json:"id"`
						} `json:"rule"`
					} `json:"triggers"`
				}

				jsonText, ok := span.Tag("_dd.appsec.json").(string)
				require.True(t, ok, "expected _dd.appsec.json tag on span")

				var parsed ddAppsecJSON
				err = json.Unmarshal([]byte(jsonText), &parsed)
				require.NoError(t, err)

				ids := make([]string, 0, len(parsed.Triggers))
				for _, trigger := range parsed.Triggers {
					ids = append(ids, trigger.Rule.ID)
				}

				require.ElementsMatch(t, ids, []string{"test-rule-001", "test-rule-002"})
			})
		}
	})

	t.Run("subscription", func(t *testing.T) {
		schema := gqlparser.MustLoadSchema(&ast.Source{Input: `type Time {
			unixTime: Int!
		}
		type Subscription {
			currentTime: Time!
		}`})

		server := handler.New(&graphql.ExecutableSchemaMock{
			ExecFunc:   execSubscriptionFunc,
			SchemaFunc: func() *ast.Schema { return schema },
		})
		server.Use(NewTracer())
		server.AddTransport(transport.Websocket{})

		client := client.New(server)

		mt := mocktracer.Start()
		defer mt.Stop()

		now := time.Now().Unix()
		sub := client.Websocket(`subscription { currentTime { unixTime } }`)
		var resp struct {
			CurrentTime struct {
				UnixTime int64 `json:"unixTime"`
			} `json:"currentTime"`
		}
		require.NoError(t, sub.Next(&resp))
		require.LessOrEqual(t, now, resp.CurrentTime.UnixTime)
		require.NoError(t, sub.Close())

		spans := mt.FinishedSpans()
		require.Len(t, spans, 3)
		require.Equal(t, "graphql.read", spans[0].OperationName())
		require.Equal(t, "graphql.parse", spans[1].OperationName())
		require.Equal(t, "graphql.validate", spans[2].OperationName())
	})
}

type appSecQuery struct{}

func (q *appSecQuery) TopLevel(_ context.Context, args struct{ ID string }) (*appSecTopLevel, error) {
	return &appSecTopLevel{args.ID}, nil
}
func (q *appSecQuery) TopLevelMapped(
	ctx context.Context,
	args struct {
		Map struct {
			IDs   []string
			Bool  bool
			Float float64
		}
		Key   string
		Index int32
	},
) (*appSecTopLevel, error) {
	id := args.Map.IDs[args.Index]
	return q.TopLevel(ctx, struct{ ID string }{id})
}

type appSecTopLevel struct {
	id string
}

func (a *appSecTopLevel) Nested(_ context.Context, args struct{ ID string }) (string, error) {
	return fmt.Sprintf("%s/%s", a.id, args.ID), nil
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
	t.Setenv("DD_APPSEC_ENABLED", "1")
	t.Setenv("DD_APPSEC_RULES", rulesFile)
	testutils.StartAppSec(t)
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}
	if !instr.AppSecEnabled() {
		cleanup()
		t.Skip("could not enable appsec: this platform is likely not supported")
	}
	return cleanup
}

func execFunc(ctx context.Context) graphql.ResponseHandler {
	type topLevel struct {
		id string
	}
	op := graphql.GetOperationContext(ctx)
	switch op.Operation.Operation {
	case ast.Query:
		return func(ctx context.Context) *graphql.Response {
			fields := graphql.CollectFields(op, op.Operation.SelectionSet, []string{"Query"})
			var (
				val    = make(map[string]any, len(fields))
				errors gqlerror.List
			)
			for _, field := range fields {
				ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
					Object: "Query",
					Field:  field,
					Args:   field.ArgumentMap(op.Variables),
				})
				fieldVal, err := op.ResolverMiddleware(ctx, func(_ context.Context) (any, error) {
					switch field.Name {
					case "topLevel":
						arg := field.Arguments.ForName("id")
						id, err := arg.Value.Value(op.Variables)
						return &topLevel{id.(string)}, err
					case "topLevelMapped":
						obj, err := field.Arguments.ForName("map").Value.Value(op.Variables)
						if err != nil {
							return nil, err
						}
						key, err := field.Arguments.ForName("key").Value.Value(op.Variables)
						if err != nil {
							return nil, err
						}
						index, err := field.Arguments.ForName("index").Value.Value(op.Variables)
						if err != nil {
							return nil, err
						}
						id := ((obj.(map[string]any))[key.(string)].([]any))[index.(int64)]
						return &topLevel{id.(string)}, nil
					default:
						return nil, fmt.Errorf("unknown field: %s", field.Name)
					}
				})
				if err != nil {
					errors = append(errors, gqlerror.Errorf("%v", err))
				} else {
					redux := make(map[string]any, len(field.SelectionSet))
					for _, nested := range graphql.CollectFields(op, field.SelectionSet, []string{"TopLevel"}) {
						ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
							Object: "TopLevel",
							Field:  nested,
							Args:   nested.ArgumentMap(op.Variables),
						})
						nestedVal, err := op.ResolverMiddleware(ctx, func(_ context.Context) (any, error) {
							switch nested.Name {
							case "nested":
								arg := nested.Arguments.ForName("id")
								id, err := arg.Value.Value(op.Variables)
								return fmt.Sprintf("%s/%s", fieldVal.(*topLevel).id, id.(string)), err
							default:
								return nil, fmt.Errorf("unknown field: %s", nested.Name)
							}
						})
						if err != nil {
							errors = append(errors, gqlerror.Errorf("%v", err))
						} else {
							redux[nested.Alias] = nestedVal
						}
					}
					val[field.Alias] = redux
				}
			}
			data, err := json.Marshal(val)
			if err != nil {
				errors = append(errors, gqlerror.Errorf("%v", err))
			}
			return &graphql.Response{
				Data:   data,
				Errors: errors,
			}
		}
	default:
		return graphql.OneShot(graphql.ErrorResponse(ctx, "not implemented"))
	}
}

func execSubscriptionFunc(ctx context.Context) graphql.ResponseHandler {
	op := graphql.GetOperationContext(ctx)

	if op.Operation.Operation != ast.Subscription {
		return graphql.OneShot(graphql.ErrorResponse(ctx, "not implemented"))
	}

	return func(ctx context.Context) *graphql.Response {
		fields := graphql.CollectFields(op, op.Operation.SelectionSet, []string{"Subscription"})
		resp := make(map[string]any)
		for _, field := range fields {
			switch field.Name {
			case "currentTime":
				resp["currentTime"] = map[string]int64{
					"unixTime": time.Now().Unix(),
				}
			default:
				return graphql.ErrorResponse(ctx, "unknown field: %s", field.Name)
			}
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return graphql.ErrorResponse(ctx, "failed to marshal response: %v", err)
		}
		return &graphql.Response{Data: data}
	}
}
