// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/DataDog/dd-trace-go/v2/profiler"

	"github.com/stretchr/testify/assert"
)

func mockGlobalClient(client telemetry.Client) func() {
	orig := telemetry.GlobalClient()
	telemetry.SwapClient(client)
	return func() {
		telemetry.SwapClient(orig)
	}
}

func checkConfig(t *testing.T, cfgs []telemetry.Configuration, key string, value any) {
	for _, c := range cfgs {
		if c.Name == key && reflect.DeepEqual(c.Value, value) {
			return
		}
	}

	t.Fatalf("could not find configuration key %s with value %v", key, value)
}

func TestTelemetryEnabled(t *testing.T) {
	t.Run("tracer start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		Start(
			WithDebugMode(true),
			WithService("test-serv"),
			WithEnv("test-env"),
			WithRuntimeMetrics(),
			WithPeerServiceMapping("key", "val"),
			WithPeerServiceDefaults(true),
			WithDebugStack(false),
			WithHeaderTags([]string{"key:val", "key2:val2"}),
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

		assert.True(t, telemetryClient.Started)
		telemetryClient.AssertNumberOfCalls(t, "ApplyOps", 1)
		checkConfig(t, telemetryClient.Configuration, "trace_debug_enabled", true)
		checkConfig(t, telemetryClient.Configuration, "service", "test-serv")
		checkConfig(t, telemetryClient.Configuration, "env", "test-env")
		checkConfig(t, telemetryClient.Configuration, "runtime_metrics_enabled", true)
		checkConfig(t, telemetryClient.Configuration, "stats_computation_enabled", false)
		checkConfig(t, telemetryClient.Configuration, "trace_enabled", true)
		checkConfig(t, telemetryClient.Configuration, "trace_span_attribute_schema", 0)
		checkConfig(t, telemetryClient.Configuration, "trace_peer_service_defaults_enabled", true)
		checkConfig(t, telemetryClient.Configuration, "trace_peer_service_mapping", "key:val")
		checkConfig(t, telemetryClient.Configuration, "debug_stack_enabled", false)
		checkConfig(t, telemetryClient.Configuration, "orchestrion_enabled", false)
		checkConfig(t, telemetryClient.Configuration, "trace_sample_rate", nil) // default value is NaN which is sanitized to nil
		checkConfig(t, telemetryClient.Configuration, "trace_header_tags", "key:val,key2:val2")
		checkConfig(t, telemetryClient.Configuration, "trace_sample_rules",
			`[{"service":"test-serv","name":"op-name","resource":"resource-*","sample_rate":0.1,"tags":{"tag-a":"tv-a??"}}]`)
		checkConfig(t, telemetryClient.Configuration, "span_sample_rules", "[]")
		assert.NotZero(t, telemetryClient.Distribution(telemetry.NamespaceTracers, "init_time", nil).Get())
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

			telemetryClient := new(telemetrytest.MockClient)
			defer mockGlobalClient(telemetryClient)()
			Start(WithService("test-serv"),
				WithSamplingRules([]SamplingRule{rule}),
			)
			defer globalconfig.SetServiceName("")
			defer Stop()

			assert.True(t, telemetryClient.Started)
			checkConfig(t, telemetryClient.Configuration, "trace_sample_rules",
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

		telemetryClient := new(telemetrytest.MockClient)
		defer mockGlobalClient(telemetryClient)()
		Start(WithService("test-serv"),
			WithSamplingRules(rules),
		)
		defer globalconfig.SetServiceName("")
		defer Stop()

		assert.True(t, telemetryClient.Started)
		checkConfig(t, telemetryClient.Configuration, "trace_sample_rules",
			`[{"service":"test-serv","name":"op-name","resource":"resource-*","sample_rate":0.1,"tags":{"tag-a":"tv-a??"}}]`)
		checkConfig(t, telemetryClient.Configuration, "span_sample_rules",
			`[{"service":"test-serv","name":"op-name","sample_rate":0.1}]`)
	})

	t.Run("tracer start with empty rules", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer mockGlobalClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLING_RULES", "")
		t.Setenv("DD_SPAN_SAMPLING_RULES", "")
		Start()
		defer globalconfig.SetServiceName("")
		defer Stop()

		assert.True(t, telemetryClient.Started)
		var cfgs []telemetry.Configuration
		for _, c := range telemetryClient.Configuration {
			c.Value = telemetry.SanitizeConfigValue(c.Value)
			cfgs = append(cfgs)
		}
		checkConfig(t, cfgs, "trace_sample_rules", "[]")
		checkConfig(t, cfgs, "span_sample_rules", "[]")
	})

	t.Run("profiler start, tracer start", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer mockGlobalClient(telemetryClient)()
		profiler.Start()
		defer profiler.Stop()
		Start(
			WithService("test-serv"),
		)
		defer globalconfig.SetServiceName("")
		defer Stop()
		checkConfig(t, telemetryClient.Configuration, "service", "test-serv")
		telemetryClient.AssertNumberOfCalls(t, "ApplyOps", 2)
	})
	t.Run("orchestrion telemetry", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer mockGlobalClient(telemetryClient)()

		Start(WithOrchestrion(map[string]string{"k1": "v1", "k2": "v2"}))
		defer Stop()

		checkConfig(t, telemetryClient.Configuration, "orchestrion_enabled", true)
		checkConfig(t, telemetryClient.Configuration, "orchestrion_k1", "v1")
		checkConfig(t, telemetryClient.Configuration, "orchestrion_k2", "v2")
	})
}
