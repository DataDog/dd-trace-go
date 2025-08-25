// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package appsec

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/timer"
	"github.com/stretchr/testify/require"
)

func TestDetectLibDL(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	restore := telemetry.MockClient(client)
	defer restore()

	prevLevel := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(prevLevel)

	if ok, _ := libddwaf.Usable(); !ok {
		t.Skip("WAF is not usable, skipping test")
	}

	if runtime.GOOS != "linux" {
		t.Skip("This test is only relevant for Linux")
	}

	detectLibDL()

	telemetrytest.CheckConfig(t, client.Configuration, "libdl_present", true)
}

func TestAPISecuritySchemaCollection(t *testing.T) {
	if wafOk, err := libddwaf.Usable(); !wafOk {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}
	builder, err := libddwaf.NewBuilder("", "")
	require.NoError(t, err)
	defer builder.Close()

	_, err = builder.AddDefaultRecommendedRuleset()
	require.NoError(t, err)

	handle := builder.Build()
	require.NotNil(t, handle)
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
			wafCtx, err := handle.NewContext(timer.WithBudget(time.Second))
			require.NoError(t, err)
			defer wafCtx.Close()
			runData := libddwaf.RunAddressData{
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
				addresses.ServerRequestHeadersNoCookiesAddr: map[string][]string{
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
				addresses.ServerRequestPathParamsAddr: map[string]string{
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
				addresses.ServerRequestQueryAddr: map[string][]string{"my-query": {"is-beautiful"}, "my-query-2": {"so-pretty"}},
			},
			tags: map[string]string{
				"_dd.appsec.s.req.query": `[{"my-query":[[[8]],{"len":1}],"my-query-2":[[[8]],{"len":1}]}]`,
			},
		},
		{
			name: "combined",
			addresses: map[string]any{
				addresses.ServerRequestHeadersNoCookiesAddr: map[string][]string{
					"my-header": {"is-beautiful"},
				},
				addresses.ServerRequestPathParamsAddr: map[string]string{
					"my-path-param": "is-beautiful",
				},
				addresses.ServerRequestQueryAddr: map[string][]string{"my-query": {"is-beautiful"}, "my-query-2": {"so-pretty"}},
			},
			tags: map[string]string{
				"_dd.appsec.s.req.headers": `[{"my-header":[[[8]],{"len":1}]}]`,
				"_dd.appsec.s.req.params":  `[{"my-path-param":[8]}]`,
				"_dd.appsec.s.req.query":   `[{"my-query":[[[8]],{"len":1}],"my-query-2":[[[8]],{"len":1}]}]`,
			},
		},
	} {
		t.Run("tags/"+tc.name, func(t *testing.T) {
			wafCtx, err := handle.NewContext(timer.WithBudget(time.Second))
			require.NoError(t, err)
			defer wafCtx.Close()

			runData := libddwaf.RunAddressData{
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
			for k, v := range wafRes.Derivatives {
				expected, checked := tc.tags[k]
				if !checked {
					continue
				}
				res, err := json.Marshal(v)
				require.NoError(t, err)
				require.Equal(t, expected, string(res))
			}
		})
	}
}
