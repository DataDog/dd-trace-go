// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package appsec

import (
	"encoding/json"
	internal "github.com/DataDog/appsec-internal-go/appsec"
	waf "github.com/DataDog/go-libddwaf/v3"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPISecuritySchemaCollection(t *testing.T) {
	if wafOk, err := waf.Health(); !wafOk {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}
	rules, err := internal.DefaultRulesetMap()
	require.NoError(t, err)
	handle, err := waf.NewHandle(rules, "", "")
	require.NoError(t, err)
	defer handle.Close()

	for _, tc := range []struct {
		name       string
		pathParams map[string]any
		schema     string
	}{
		{
			name: "string",
			pathParams: map[string]any{
				"param": "string proxy value",
			},
			schema: `{"_dd.appsec.s.req.params":[{"param":[8]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
		{
			name: "int",
			pathParams: map[string]any{
				"param": 10,
			},
			schema: `{"_dd.appsec.s.req.params":[{"param":[4]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
		{
			name: "float",
			pathParams: map[string]any{
				"param": 10.0,
			},
			schema: `{"_dd.appsec.s.req.params":[{"param":[16]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
		{
			name: "bool",
			pathParams: map[string]any{
				"param": true,
			},
			schema: `{"_dd.appsec.s.req.params":[{"param":[2]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
		{
			name: "record",
			pathParams: map[string]any{
				"param": map[string]any{"recordKey": "recordValue"},
			},
			schema: `{"_dd.appsec.s.req.params":[{"param":[{"recordKey":[8]}]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
		{
			name: "array",
			pathParams: map[string]any{
				"param": []any{"arrayVal1", 10, false, 10.0},
			},
			schema: `{"_dd.appsec.s.req.params":[{"param":[[[2],[16],[4],[8]],{"len":4}]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
		{
			name: "vin-scanner",
			pathParams: map[string]any{
				"vin": "AAAAAAAAAAAAAAAAA",
			},
			schema: `{"_dd.appsec.s.req.params":[{"vin":[8,{"category":"pii","type":"vin"}]}],"_dd.appsec.s.req.query":[{"query":[[[8]],{"len":1}]}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wafCtx, err := handle.NewContext()
			require.NoError(t, err)
			defer wafCtx.Close()
			runData := waf.RunAddressData{
				Persistent: map[string]any{
					"waf.context.processor":      map[string]any{"extract-schema": true},
					"server.request.path_params": tc.pathParams,
					"server.request.query": map[string][]string{
						"query": {"$http_server_vars"},
					},
				},
			}
			res, err := wafCtx.Run(runData)
			require.NoError(t, err)
			require.NotNil(t, res)
			require.True(t, res.HasDerivatives())
			schema, err := json.Marshal(res.Derivatives)
			require.NoError(t, err)
			require.Equal(t, tc.schema, string(schema))
		})
	}

	for _, tc := range []struct {
		name      string
		addresses map[string]any
		tags      map[string]string
	}{
		{
			name: "headers",
			addresses: map[string]any{
				httpsec.ServerRequestHeadersNoCookiesAddr: map[string][]string{
					"my-header": {"is-beautiful"},
				},
			},
			tags: map[string]string{
				"_dd.appsec.s.req.headers": `[{"my-header":[[[8]],{"len":1}]}]`,
			},
		},
		{
			name: "path-params",
			addresses: map[string]any{
				httpsec.ServerRequestPathParamsAddr: map[string]string{
					"my-path-param": "is-beautiful",
				},
			},
			tags: map[string]string{
				"_dd.appsec.s.req.params": `[{"my-path-param":[8]}]`,
			},
		},
		{
			name: "query",
			addresses: map[string]any{
				httpsec.ServerRequestQueryAddr: map[string][]string{"my-query": {"is-beautiful"}, "my-query-2": {"so-pretty"}},
			},
			tags: map[string]string{
				"_dd.appsec.s.req.query": `[{"my-query":[[[8]],{"len":1}],"my-query-2":[[[8]],{"len":1}]}]`,
			},
		},
		{
			name: "combined",
			addresses: map[string]any{
				httpsec.ServerRequestHeadersNoCookiesAddr: map[string][]string{
					"my-header": {"is-beautiful"},
				},
				httpsec.ServerRequestPathParamsAddr: map[string]string{
					"my-path-param": "is-beautiful",
				},
				httpsec.ServerRequestQueryAddr: map[string][]string{"my-query": {"is-beautiful"}, "my-query-2": {"so-pretty"}},
			},
			tags: map[string]string{
				"_dd.appsec.s.req.headers": `[{"my-header":[[[8]],{"len":1}]}]`,
				"_dd.appsec.s.req.params":  `[{"my-path-param":[8]}]`,
				"_dd.appsec.s.req.query":   `[{"my-query":[[[8]],{"len":1}],"my-query-2":[[[8]],{"len":1}]}]`,
			},
		},
	} {
		t.Run("tags/"+tc.name, func(t *testing.T) {
			wafCtx, err := handle.NewContext()
			require.NoError(t, err)
			defer wafCtx.Close()

			runData := waf.RunAddressData{
				Ephemeral: map[string]any{
					"waf.context.processor": map[string]any{"extract-schema": true},
				},
			}
			for k, v := range tc.addresses {
				runData.Ephemeral[k] = v
			}

			wafRes, err := wafCtx.Run(runData)
			require.NoError(t, err)
			require.True(t, wafRes.HasDerivatives())
			tagsHolder := trace.NewTagsHolder()
			for k, v := range wafRes.Derivatives {
				tagsHolder.AddSerializableTag(k, v)
			}

			for tag, val := range tagsHolder.Tags() {
				require.Equal(t, tc.tags[tag], val)
			}
		})
	}
}
