// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/DataDog/dd-trace-go/v2/profiler"

	"github.com/stretchr/testify/assert"
)

func TestTelemetryEnabled(t *testing.T) {
	t.Run("tracer start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		// Create mock agent server with /info endpoint
		// stats_computation_enabled depends on the trace-agent exposing this endpoint
		mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/info" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"endpoints": ["/v0.4/traces", "/v0.6/stats"]}`))
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer mockAgent.Close()

		agentURL, err := url.Parse(mockAgent.URL)
		assert.NoError(t, err)

		Start(
			WithStatsComputation(true),
			WithDebugMode(true),
			WithService("test-serv"),
			WithEnv("test-env"),
			WithRuntimeMetrics(),
			WithPeerServiceMapping("key", "val"),
			WithPeerServiceDefaults(true),
			WithDebugStack(false),
			WithHeaderTags([]string{"key:val", "key2:val2"}),
			WithAgentAddr(agentURL.Host),
			WithSamplingRules(
				TraceSamplingRules(
					Rule{
						Tags:         map[string]string{"tag-a": "tv-a??"},
						ResourceGlob: "resource-*",
						NameGlob:     "op-name",
						ServiceGlob:  "test-serv",
						Rate:         0.1,
					},
				),
			),
		)
		defer globalconfig.SetServiceName("")
		defer Stop()

		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_debug_enabled", true)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "service", "test-serv")
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "env", "test-env")
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "runtime_metrics_enabled", true)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "stats_computation_enabled", true)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_enabled", true)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_span_attribute_schema", 0)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_peer_service_defaults_enabled", true)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_peer_service_mapping", "key:val")
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "debug_stack_enabled", false)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "orchestrion_enabled", false)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_sample_rate", nil) // default value is NaN which is sanitized to nil
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_header_tags", "key:val,key2:val2")
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_sample_rules",
			`[{"service":"test-serv","name":"op-name","resource":"resource-*","sample_rate":0.1,"tags":{"tag-a":"tv-a??"}}]`)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "span_sample_rules", "[]")

		assert.NotZero(t, telemetryClient.Distribution(telemetry.NamespaceGeneral, "init_time", nil).Get())
	})

	t.Run("telemetry customer or dynamic rules", func(t *testing.T) {
		rule := TraceSamplingRules(Rule{
			Tags:         map[string]string{"tag-a": "tv-a??"},
			ResourceGlob: "resource-*",
			NameGlob:     "op-name",
			ServiceGlob:  "test-serv",
			Rate:         0.1,
		})[0]

		for _, prov := range provenances {
			if prov == Local {
				continue
			}
			rule.Provenance = prov

			telemetryClient := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(telemetryClient)()
			Start(WithService("test-serv"),
				WithSamplingRules([]SamplingRule{rule}),
			)
			defer globalconfig.SetServiceName("")
			defer Stop()

			telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_sample_rules",
				fmt.Sprintf(`[{"service":"test-serv","name":"op-name","resource":"resource-*","sample_rate":0.1,"tags":{"tag-a":"tv-a??"},"provenance":"%s"}]`, prov.String()))
		}
	})

	t.Run("telemetry local rules", func(t *testing.T) {
		rules := TraceSamplingRules(
			Rule{Tags: map[string]string{"tag-a": "tv-a??"}, ResourceGlob: "resource-*", NameGlob: "op-name", ServiceGlob: "test-serv", Rate: 0.1},
		)
		rules = append(rules, SpanSamplingRules(
			// Span rules can have only local provenance for now.
			Rule{NameGlob: "op-name", ServiceGlob: "test-serv", Rate: 0.1},
		)...)

		for i := range rules {
			rules[i].Provenance = Local
		}

		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()
		Start(WithService("test-serv"),
			WithSamplingRules(rules),
		)
		defer globalconfig.SetServiceName("")
		defer Stop()

		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_sample_rules",
			`[{"service":"test-serv","name":"op-name","resource":"resource-*","sample_rate":0.1,"tags":{"tag-a":"tv-a??"}}]`)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "span_sample_rules",
			`[{"service":"test-serv","name":"op-name","sample_rate":0.1}]`)
	})

	t.Run("tracer start with empty rules", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLING_RULES", "")
		t.Setenv("DD_SPAN_SAMPLING_RULES", "")
		Start()
		defer globalconfig.SetServiceName("")
		defer Stop()

		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "trace_sample_rules", "[]")
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "span_sample_rules", "[]")
	})

	t.Run("profiler start, tracer start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()
		profiler.Start()
		defer profiler.Stop()
		Start(
			WithService("test-serv"),
		)
		defer globalconfig.SetServiceName("")
		defer Stop()
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "service", "test-serv")
	})
	t.Run("orchestrion telemetry", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		Start(func(c *config) {
			c.orchestrionCfg = orchestrionConfig{
				Enabled:  true,
				Metadata: &orchestrionMetadata{Version: "v1337.42.0-phony"},
			}
		})
		defer Stop()

		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "orchestrion_enabled", true)
		telemetrytest.CheckConfig(t, telemetryClient.Configuration, "orchestrion_version", "v1337.42.0-phony")
	})
}
