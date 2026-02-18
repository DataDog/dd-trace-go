// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
)

func assertCalled(t *testing.T, client *telemetrytest.RecordClient, cfgs []telemetry.Configuration) {
	t.Helper()

	for _, cfg := range cfgs {
		assert.Contains(t, client.Configuration, cfg)
	}
}

func TestOnRemoteConfigUpdate(t *testing.T) {
	t.Run("RC sampling rate = 0.5 is applied and can be reverted", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)

		// Apply RC. Assert _dd.rule_psr shows the RC sampling rate (0.5) is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_sampling_rate": 0.5}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request")
		s.Finish()
		rate, _ := getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.5, rate)

		// Telemetry
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: 0.5, Origin: telemetry.OriginRemoteConfig})

		// Apply RC with sampling rules. Assert _dd.rule_psr shows the corresponding rule matched rate.
		input = remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.5,
			"tracing_sampling_rules":[{
				"service": "my-service",
				"name": "web.request",
				"resource": "*",
				"provenance": "customer",
				"sample_rate": 1.0
			}]},
			"service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 1.0, rate)
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])
		// Spans not matching the rule still gets the global rate
		s = tracer.StartSpan("not.web.request")
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.5, rate)
		if p, ok := s.context.trace.samplingPriority(); ok && p > 0 {
			require.Equal(t, samplerToDM(samplernames.RuleRate), s.context.trace.propagatingTags[keyDecisionMaker])
		}

		// Unset RC. Assert _dd.rule_psr is not set
		input = remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.Finish()
		require.NotContains(t, keyRulesSamplerAppliedRate, s.metrics)

		// assert telemetry config contains trace_sample_rate with Nan (marshalled as nil)
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: nil, Origin: telemetry.OriginDefault})
	})

	t.Run("DD_TRACE_SAMPLE_RATE=0.1 and RC sampling rate = 0.2", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.1")
		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginEnvVar, tracer.config.traceSampleRate.cfgOrigin)

		// Apply RC. Assert _dd.rule_psr shows the RC sampling rate (0.2) is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_sampling_rate": 0.2}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request")
		s.Finish()
		rate, _ := getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.2, rate)

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: 0.2, Origin: telemetry.OriginRemoteConfig})

		// Unset RC. Assert _dd.rule_psr shows the previous sampling rate (0.1) is applied
		input = remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.1, rate)

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: 0.1, Origin: telemetry.OriginDefault})
	})

	t.Run("DD_TRACE_SAMPLING_RULES rate=0.1 and RC trace sampling rules rate = 1.0", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLING_RULES", `[{
				"service": "my-service",
				"name": "web.request",
				"resource": "*",
				"sample_rate": 0.1
			}]`)
		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)

		s := tracer.StartSpan("web.request")
		s.Finish()
		rate, _ := getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.1, rate)
		if p, ok := s.context.trace.samplingPriority(); ok && p > 0 {
			require.Equal(t, samplerToDM(samplernames.RuleRate), s.context.trace.propagatingTags[keyDecisionMaker])
		}

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.5,
			"tracing_sampling_rules":[{
				"service": "my-service",
				"name": "web.request",
				"resource": "abc",
				"provenance": "customer",
				"sample_rate": 1.0
			}]},
			"service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.resource = "abc"
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 1.0, rate)
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])
		// Spans not matching the rule gets the global rate, but not the local rule, which is no longer in effect
		s = tracer.StartSpan("web.request")
		s.resource = "not_abc"
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.5, rate)
		if p, ok := s.context.trace.samplingPriority(); ok && p > 0 {
			require.Equal(t, samplerToDM(samplernames.RuleRate), s.context.trace.propagatingTags[keyDecisionMaker])
		}

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: 0.5, Origin: telemetry.OriginRemoteConfig})
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{
			Name:   "trace_sample_rules",
			Value:  `[{"service":"my-service","name":"web.request","resource":"abc","sample_rate":1,"provenance":"customer"}]`,
			Origin: telemetry.OriginRemoteConfig,
		})
	})

	t.Run("DD_TRACE_SAMPLING_RULES=0.0 and RC rule rate=1.0 and revert", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLING_RULES", `[{
				"service": "my-service",
				"name": "web.request",
				"resource": "*",
				"sample_rate": 0.0
			}]`)
		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		require.NoError(t, err)

		s := tracer.StartSpan("web.request")
		s.Finish()
		rate, _ := getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.0, rate)
		p, ok := s.context.trace.samplingPriority()
		require.True(t, ok)
		require.Equal(t, p, -1)
		require.Empty(t, s.context.trace.propagatingTags[keyDecisionMaker])

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.5,
			"tracing_sampling_rules":[{
				"service": "my-service",
				"name": "web.request",
				"resource": "abc",
				"provenance": "customer",
				"sample_rate": 1.0
			},
		{
			"service": "my-service",
			"name": "web.request",
			"resource": "*",
			"provenance": "dynamic",
			"sample_rate": 1.0
		}]},
		"service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.resource = "abc"
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 1.0, rate)
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])
		// Spans not matching the rule gets the global rate, but not the local rule, which is no longer in effect
		s = tracer.StartSpan("web.request")
		s.resource = "not_abc"
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 1.0, rate)
		p, ok = s.context.trace.samplingPriority()
		require.True(t, ok)
		require.Equal(t, p, 2)
		require.Equal(
			t,
			samplerToDM(samplernames.RemoteDynamicRule),
			s.context.trace.propagatingTags[keyDecisionMaker],
		)

		// Reset restores local rules
		input = remoteconfig.ProductUpdate{"path": nil}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.resource = "not_abc"
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.0, rate)
		p, ok = s.context.trace.samplingPriority()
		require.True(t, ok)
		require.Equal(t, p, -1)
		require.Empty(t, s.context.trace.propagatingTags[keyDecisionMaker])

		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_sample_rate", Value: 0.5, Origin: telemetry.OriginRemoteConfig},
			{Name: "trace_sample_rules",
				Value: `[{"service":"my-service","name":"web.request","resource":"abc","sample_rate":1,"provenance":"customer"} {"service":"my-service","name":"web.request","resource":"*","sample_rate":1,"provenance":"dynamic"}]`, Origin: telemetry.OriginRemoteConfig},
		})
		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_sample_rate", Value: nil, Origin: telemetry.OriginDefault},
			{
				Name:   "trace_sample_rules",
				Value:  `[{"service":"my-service","name":"web.request","resource":"*","sample_rate":0}]`,
				Origin: telemetry.OriginDefault,
			},
		})
	})

	t.Run("RC rule with tags", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()
		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.5,
			"tracing_sampling_rules": [{
				"service": "my-service",
				"name": "web.request",
				"resource": "*",
				"provenance": "customer",
				"sample_rate": 1.0,
				"tags": [{"key": "tag-a", "value_glob": "tv-a??"}]
			}]},
			"service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)

		// A span with matching tags gets the RC rule rate
		s := tracer.StartSpan("web.request")
		s.SetTag("tag-a", "tv-a11")
		s.Finish()
		rate, _ := getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 1.0, rate)
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])

		// A span with non-matching tags gets the global rate
		s = tracer.StartSpan("web.request")
		s.SetTag("tag-a", "not-matching")
		s.Finish()
		rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
		require.Equal(t, 0.5, rate)

		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_sample_rate", Value: 0.5, Origin: telemetry.OriginRemoteConfig},
			{
				Name:   "trace_sample_rules",
				Value:  `[{"service":"my-service","name":"web.request","resource":"*","sample_rate":1,"tags":{"tag-a":"tv-a??"},"provenance":"customer"}]`,
				Origin: telemetry.OriginRemoteConfig,
			},
		})
	})

	t.Run("RC header tags = X-Test-Header:my-tag-name is applied and can be reverted", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)

		// Apply RC. Assert global config shows the RC header tag is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name"}]}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name", globalconfig.HeaderTag("X-Test-Header"))

		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name", Origin: telemetry.OriginRemoteConfig},
		})

		// Unset RC. Assert header tags are not set
		input = remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 0, globalconfig.HeaderTagsLen())

		// Telemetry
		assertCalled(t, telemetryClient,
			[]telemetry.Configuration{{Name: "trace_header_tags", Value: "", Origin: telemetry.OriginDefault}},
		)
	})

	t.Run("RC tracing_enabled = false is applied", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tr, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tr.config.traceSampleRate.cfgOrigin)

		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_enabled": false}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}

		tr, ok := getGlobalTracer().(*tracer)
		require.Equal(t, true, ok)
		applyStatus := tr.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, false, tr.config.enabled.current)
		headers := TextMapCarrier{
			traceparentHeader:      "00-12345678901234567890123456789012-1234567890123456-01",
			tracestateHeader:       "dd=s:2;o:rum;t.usr.id:baz64~~",
			"ot-baggage-something": "someVal",
		}
		sctx, err := tr.Extract(headers)
		require.NoError(t, err)
		require.Equal(t, (*SpanContext)(nil), sctx)
		err = tr.Inject(nil, TextMapCarrier{})
		require.NoError(t, err)
		require.Equal(t, (*Span)(nil), tr.StartSpan("noop"))

		// all subsequent spans are of type internal.NoopSpan
		// no further remoteConfig changes are applied
		s := StartSpan("web.request")
		s.Finish()

		// turning tracing back through reset should have no effect
		input = remoteconfig.ProductUpdate{"path": nil}
		applyStatus = tr.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, false, tr.config.enabled.current)

		// turning tracing back explicitly is not allowed
		input = remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_enabled": true}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		applyStatus = tr.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, false, tr.config.enabled.current)
	})

	t.Run(
		"DD_TRACE_HEADER_TAGS=X-Test-Header:my-tag-name-from-env and RC header tags = X-Test-Header:my-tag-name-from-rc",
		func(t *testing.T) {
			defer globalconfig.ClearHeaderTags()
			telemetryClient := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(telemetryClient)()

			t.Setenv("DD_TRACE_HEADER_TAGS", "X-Test-Header:my-tag-name-from-env")
			tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
			require.Nil(t, err)
			defer stop()

			// Apply RC. Assert global config shows the RC header tag is applied
			input := remoteconfig.ProductUpdate{
				"path": []byte(
					`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name-from-rc"}]}, "service_target": {"service": "my-service", "env": "my-env"}}`,
				),
			}
			applyStatus := tracer.onRemoteConfigUpdate(input)
			require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
			require.Equal(t, 1, globalconfig.HeaderTagsLen())
			require.Equal(t, "my-tag-name-from-rc", globalconfig.HeaderTag("X-Test-Header"))

			// Telemetry
			assertCalled(t, telemetryClient,
				[]telemetry.Configuration{
					{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-from-rc", Origin: telemetry.OriginRemoteConfig},
				},
			)

			// Unset RC. Assert global config shows the DD_TRACE_HEADER_TAGS header tag
			input = remoteconfig.ProductUpdate{
				"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
			}
			applyStatus = tracer.onRemoteConfigUpdate(input)
			require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
			require.Equal(t, 1, globalconfig.HeaderTagsLen())
			require.Equal(t, "my-tag-name-from-env", globalconfig.HeaderTag("X-Test-Header"))

			// Telemetry
			assertCalled(t, telemetryClient,
				[]telemetry.Configuration{
					{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-from-env", Origin: telemetry.OriginDefault},
				},
			)
		},
	)

	t.Run(
		"In code header tags = X-Test-Header:my-tag-name-in-code and RC header tags = X-Test-Header:my-tag-name-from-rc",
		func(t *testing.T) {
			defer globalconfig.ClearHeaderTags()
			telemetryClient := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(telemetryClient)()

			tracer, _, _, stop, err := startTestTracer(
				t,
				WithService("my-service"),
				WithEnv("my-env"),
				WithHeaderTags([]string{"X-Test-Header:my-tag-name-in-code"}),
			)
			require.Nil(t, err)
			defer stop()

			// Apply RC. Assert global config shows the RC header tag is applied
			input := remoteconfig.ProductUpdate{
				"path": []byte(
					`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name-from-rc"}]}, "service_target": {"service": "my-service", "env": "my-env"}}`,
				),
			}
			applyStatus := tracer.onRemoteConfigUpdate(input)
			require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
			require.Equal(t, 1, globalconfig.HeaderTagsLen())
			require.Equal(t, "my-tag-name-from-rc", globalconfig.HeaderTag("X-Test-Header"))

			// Telemetry
			assertCalled(t, telemetryClient,
				[]telemetry.Configuration{
					{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-from-rc", Origin: telemetry.OriginRemoteConfig},
				},
			)

			// Unset RC. Assert global config shows the in-code header tag
			input = remoteconfig.ProductUpdate{
				"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
			}
			applyStatus = tracer.onRemoteConfigUpdate(input)
			require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
			require.Equal(t, 1, globalconfig.HeaderTagsLen())
			require.Equal(t, "my-tag-name-in-code", globalconfig.HeaderTag("X-Test-Header"))

			// Telemetry
			assertCalled(t, telemetryClient,
				[]telemetry.Configuration{
					{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-in-code", Origin: telemetry.OriginDefault},
				},
			)
		},
	)

	t.Run("Invalid payload", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)

		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_sampling_rate": "string value", "service_target": {"service": "my-service", "env": "my-env"}}}`,
			),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateError, applyStatus["path"].State)
		require.NotEmpty(t, applyStatus["path"].Error)

		// Telemetry
		for _, cfg := range telemetryClient.Configuration {
			assert.NotEqual(t, "trace_sample_rate", cfg.Name)
		}
	})

	t.Run("Service mismatch", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "other-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Empty(t, applyStatus["path"].Error)

		// Telemetry
		for _, cfg := range telemetryClient.Configuration {
			assert.NotEqual(t, "trace_sample_rate", cfg.Name)
		}
	})

	t.Run("Env mismatch", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "other-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Empty(t, applyStatus["path"].Error)

		// Telemetry
		for _, cfg := range telemetryClient.Configuration {
			assert.NotEqual(t, "trace_sample_rate", cfg.Name)
		}
	})

	t.Run("DD_TAGS=key0:val0,key1:val1, WithGlobalTag=key2:val2 and RC tags = key3:val3,key4:val4", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("DD_TAGS", "key0:val0,key1:val1")
		tracer, _, _, stop, err := startTestTracer(
			t,
			WithService("my-service"),
			WithEnv("my-env"),
			WithGlobalTag("key2", "val2"),
		)
		require.Nil(t, err)
		defer stop()

		require.Equal(t, telemetry.OriginDefault, tracer.config.traceSampleRate.cfgOrigin)
		require.Equal(t, telemetry.OriginEnvVar, tracer.config.globalTags.cfgOrigin)

		// Apply RC. Assert global tags have the RC tags key3:val3,key4:val4 applied + runtime ID
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_tags": ["key3:val3","key4:val4"]}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request")
		s.Finish()
		_, ok := getMeta(s, "key0")
		require.False(t, ok)
		_, ok = getMeta(s, "key1")
		require.False(t, ok)
		_, ok = getMeta(s, "key2")
		require.False(t, ok)
		val3, _ := getMeta(s, "key3")
		val4, _ := getMeta(s, "key4")
		require.Equal(t, "val3", val3)
		require.Equal(t, "val4", val4)
		runtimeID, _ := getMeta(s, ext.RuntimeID)
		require.Equal(t, globalconfig.RuntimeID(), runtimeID)
		runtimeIDTag := ext.RuntimeID + ":" + globalconfig.RuntimeID()

		// Telemetry
		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_tags", Value: "key3:val3,key4:val4," + runtimeIDTag, Origin: telemetry.OriginRemoteConfig},
		},
		)

		// Unset RC. Assert config shows the original DD_TAGS + WithGlobalTag + runtime ID
		input = remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.Finish()
		val0, _ := getMeta(s, "key0")
		val1, _ := getMeta(s, "key1")
		val2, _ := getMeta(s, "key2")
		require.Equal(t, "val0", val0)
		require.Equal(t, "val1", val1)
		require.Equal(t, "val2", val2)
		_, ok = getMeta(s, "key3")
		require.False(t, ok)
		_, ok = getMeta(s, "key4")
		require.False(t, ok)
		runtimeID, _ = getMeta(s, ext.RuntimeID)
		require.Equal(t, globalconfig.RuntimeID(), runtimeID)

		// Telemetry
		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_tags", Value: "key0:val0,key1:val1,key2:val2," + runtimeIDTag, Origin: telemetry.OriginDefault},
		},
		)
	})

	t.Run("Deleted config", func(t *testing.T) {
		rcSamplingRate := 0.2
		rcHeaderTag := "my-tag-from-rc"
		rcSpanTag := "from-rc"
		samplingRatePath := "pathSamplingRate"
		samplingRateConfig := []byte(
			fmt.Sprintf(
				`{"lib_config": {"tracing_sampling_rate": %f}, 
					"service_target": {"service": "my-service", "env": "my-env"}}`,
				rcSamplingRate))
		tagsPath := "pathTags"
		tagsConfig := []byte(
			fmt.Sprintf(
				`{"lib_config": {"tracing_tags": ["ddtag:%s"], "tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "%s"}]}, 
					"service_target": {"service": "my-service", "env": "my-env"}}`,
				rcSpanTag,
				rcHeaderTag,
			))

		// Every test will remove some or all of the configs and assert that some
		// configuration fields get reset to the original values.
		removeTests := []struct {
			name                 string
			input                remoteconfig.ProductUpdate
			expectedSamplingRate float64
			expectedHeaderTag    string
			expectedSpanTag      string
		}{
			{
				name: "remove only one of the configs",
				input: remoteconfig.ProductUpdate{
					samplingRatePath: nil,
					tagsPath:         tagsConfig,
				},
				expectedSamplingRate: 0.1,
				expectedHeaderTag:    "my-tag-from-rc",
				expectedSpanTag:      "from-rc",
			},
			{
				name: "remove both configs by replacing them with nil values",
				input: remoteconfig.ProductUpdate{
					samplingRatePath: nil,
					tagsPath:         nil,
				},
				expectedSamplingRate: 0.1,
				expectedHeaderTag:    "my-tag-from-env",
				expectedSpanTag:      "from-env",
			},
			{
				name:                 "remove both configs by removing the paths",
				input:                remoteconfig.ProductUpdate{},
				expectedSamplingRate: 0.1,
				expectedHeaderTag:    "my-tag-from-env",
				expectedSpanTag:      "from-env",
			},
		}
		for _, tt := range removeTests {
			t.Run(tt.name, func(t *testing.T) {
				defer globalconfig.ClearHeaderTags()
				telemetryClient := new(telemetrytest.RecordClient)
				defer telemetry.MockClient(telemetryClient)()

				t.Setenv("DD_TRACE_SAMPLE_RATE", "0.1")
				t.Setenv("DD_TRACE_HEADER_TAGS", "X-Test-Header:my-tag-from-env")
				t.Setenv("DD_TAGS", "ddtag:from-env")
				tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
				assert.Nil(t, err)
				defer stop()

				require.Equal(t, telemetry.OriginEnvVar, tracer.config.traceSampleRate.cfgOrigin)

				// Apply RC. Assert configuration is updated to the RC values.
				initialInput := remoteconfig.ProductUpdate{
					samplingRatePath: samplingRateConfig,
					tagsPath:         tagsConfig,
				}
				applyStatus := tracer.onRemoteConfigUpdate(initialInput)
				require.Equal(t, state.ApplyStateAcknowledged, applyStatus[samplingRatePath].State)
				require.Equal(t, state.ApplyStateAcknowledged, applyStatus[tagsPath].State)
				s := tracer.StartSpan("web.request")
				s.Finish()
				rate, _ := getMetric(s, keyRulesSamplerAppliedRate)
				require.Equal(t, 0.2, rate)
				require.Equal(t, "my-tag-from-rc", globalconfig.HeaderTag("X-Test-Header"))
				ddTag, _ := getMeta(s, "ddtag")
				require.Equal(t, "from-rc", ddTag)
				// Telemetry
				assertCalled(t, telemetryClient, []telemetry.Configuration{
					{Name: "trace_sample_rate", Value: 0.2, Origin: telemetry.OriginRemoteConfig},
					{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-from-rc", Origin: telemetry.OriginRemoteConfig},
					{
						Name:   "trace_tags",
						Value:  "ddtag:from-rc," + ext.RuntimeID + ":" + globalconfig.RuntimeID(),
						Origin: telemetry.OriginRemoteConfig,
					},
				})

				// Apply the RC update.
				applyStatus = tracer.onRemoteConfigUpdate(tt.input)
				require.Len(t, applyStatus, len(tt.input))
				for path := range applyStatus {
					require.Equal(t, state.ApplyStateAcknowledged, applyStatus[path].State)
				}

				s = tracer.StartSpan("web.request")
				s.Finish()
				rate, _ = getMetric(s, keyRulesSamplerAppliedRate)
				require.Equal(t, tt.expectedSamplingRate, rate)
				require.Equal(t, tt.expectedHeaderTag, globalconfig.HeaderTag("X-Test-Header"))
				ddTag, _ = getMeta(s, "ddtag")
				require.Equal(t, tt.expectedSpanTag, ddTag)

				// Check expected telemetry.
				samplingRateOrigin := telemetry.OriginDefault
				if tt.expectedSamplingRate == rcSamplingRate {
					samplingRateOrigin = telemetry.OriginRemoteConfig
				}
				headerTagOrigin := telemetry.OriginDefault
				if tt.expectedHeaderTag == rcHeaderTag {
					headerTagOrigin = telemetry.OriginRemoteConfig
				}
				spanTagOrigin := telemetry.OriginDefault
				if tt.expectedSpanTag == rcSpanTag {
					spanTagOrigin = telemetry.OriginRemoteConfig
				}
				assertCalled(t, telemetryClient, []telemetry.Configuration{
					{Name: "trace_sample_rate", Value: tt.expectedSamplingRate, Origin: samplingRateOrigin},
					{Name: "trace_header_tags", Value: "X-Test-Header:" + tt.expectedHeaderTag, Origin: headerTagOrigin},
					{Name: "trace_tags", Value: "ddtag:" + tt.expectedSpanTag + "," + ext.RuntimeID + ":" + globalconfig.RuntimeID(), Origin: spanTagOrigin},
				})
			})
		}
	})

	// Check that toggling Live Debugger through RC works.
	t.Run("toggle Live Debugger through RC", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		startRemoteConfig := func(tracer *tracer) {
			t.Cleanup(remoteconfig.Reset)
			t.Cleanup(remoteconfig.Stop)
			err := tracer.startRemoteConfig(remoteconfig.DefaultClientConfig())
			require.NoError(t, err)
		}

		tr, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		defer stop()
		startRemoteConfig(tr)

		checkLiveDebuggerRemoteConfigState := func(enabled bool) {
			found, err := remoteconfig.HasProduct(state.ProductLiveDebugging)
			require.NoError(t, err)
			require.Equal(t, enabled, found)
		}

		// Tracer starts off as not subscribed to the LIVE_DEBUGGING product.
		checkLiveDebuggerRemoteConfigState(false)

		// Enable Live Debugger through RC and check that we subscribe to the
		// LIVE_DEBUGGING product.
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"dynamic_instrumentation_enabled": true}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		tr.onRemoteConfigUpdate(input)
		// Tracer is now subscribed.
		checkLiveDebuggerRemoteConfigState(true)

		// Disable Live Debugger through RC and check that we unsubscribe from the
		// LIVE_DEBUGGING product.
		input = remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"dynamic_instrumentation_enabled": false}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		tr.onRemoteConfigUpdate(input)
		// Tracer is back to not subscribed.
		checkLiveDebuggerRemoteConfigState(false)

		// Enable Live Debugger through RC again, and check that we re-subscribe to the
		// LIVE_DEBUGGING product.
		input = remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"dynamic_instrumentation_enabled": true}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		tr.onRemoteConfigUpdate(input)
		checkLiveDebuggerRemoteConfigState(true)
	})

	// Test that Live Debugger cannot be enabled through RC if the tracer has
	// explicitly disabled it.
	t.Run("enable Live Debugger through RC with tracer explicitly disabling it", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		startRemoteConfig := func(tracer *tracer) {
			t.Cleanup(remoteconfig.Reset)
			t.Cleanup(remoteconfig.Stop)
			err := tracer.startRemoteConfig(remoteconfig.DefaultClientConfig())
			require.NoError(t, err)
		}

		tr, _, _, stop, err := startTestTracer(t,
			WithService("my-service"), WithEnv("my-env"),
			// Configure the tracer to explicitly disable dynamic instrumentation.
			WithDynamicInstrumentationEnabled(false),
		)
		require.Nil(t, err)
		defer stop()
		startRemoteConfig(tr)

		checkLiveDebuggerRemoteConfigState := func(enabled bool) {
			found, err := remoteconfig.HasProduct(state.ProductLiveDebugging)
			require.NoError(t, err)
			require.Equal(t, enabled, found)
		}

		// Tracer starts off as not subscribed to the LIVE_DEBUGGING product.
		checkLiveDebuggerRemoteConfigState(false)

		// Enable Live Debugger through RC and check that we subscribe to the
		// LIVE_DEBUGGING product.
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"dynamic_instrumentation_enabled": true}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		tr.onRemoteConfigUpdate(input)
		// Tracer is still not subscribed; the remote config update did not override
		// the tracer's explicit configuration.
		checkLiveDebuggerRemoteConfigState(false)
	})

	assert.Equal(t, 0, globalconfig.HeaderTagsLen())
}

func TestDynamicInstrumentationRC(t *testing.T) {
	getDiRCState := func() map[string]dynamicInstrumentationRCProbeConfig {
		diRCState.mu.Lock()
		defer diRCState.mu.Unlock()
		return maps.Clone(diRCState.state)
	}
	getDiSymDBEnabled := func() bool {
		diRCState.mu.Lock()
		defer diRCState.mu.Unlock()
		return diRCState.symdbExport
	}
	resetDiRCState := func() {
		diRCState.mu.Lock()
		defer diRCState.mu.Unlock()
		diRCState.state = map[string]dynamicInstrumentationRCProbeConfig{}
		diRCState.symdbExport = false
	}

	startTracer := func(t *testing.T) *tracer {
		telemetryClient := new(telemetrytest.RecordClient)
		t.Cleanup(telemetry.MockClient(telemetryClient))
		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		require.Nil(t, err)
		t.Cleanup(resetDiRCState)
		t.Cleanup(stop)
		return tracer
	}

	startRemoteConfig := func(t *testing.T, tracer *tracer) {
		t.Cleanup(remoteconfig.Reset)
		t.Cleanup(remoteconfig.Stop)
		err := tracer.startRemoteConfig(remoteconfig.DefaultClientConfig())
		require.NoError(t, err)
	}

	checkRemoteConfigProductState := func(t *testing.T, product string, enabled bool) {
		found, err := remoteconfig.HasProduct(product)
		require.NoError(t, err)
		require.Equal(t, enabled, found)
	}

	t.Run("Subscribes to LIVE_DEBUGGING product when enabled", func(t *testing.T) {
		t.Setenv("DD_DYNAMIC_INSTRUMENTATION_ENABLED", "true")
		tracer := startTracer(t)
		startRemoteConfig(t, tracer)
		checkRemoteConfigProductState(t, state.ProductLiveDebugging, true)
		checkRemoteConfigProductState(t, state.ProductLiveDebuggingSymbolDB, true)
	})

	t.Run("Does not subscribe to LIVE_DEBUGGING product when disabled", func(t *testing.T) {
		tracer := startTracer(t)
		startRemoteConfig(t, tracer)
		checkRemoteConfigProductState(t, state.ProductLiveDebugging, false)
		checkRemoteConfigProductState(t, state.ProductLiveDebuggingSymbolDB, false)
	})

	t.Run("Deleted config removes from map", func(t *testing.T) {
		t.Setenv("DD_DYNAMIC_INSTRUMENTATION_ENABLED", "true")
		tracer := startTracer(t)
		startRemoteConfig(t, tracer)

		require.Empty(t, getDiRCState())
		status := tracer.dynamicInstrumentationRCUpdate(remoteconfig.ProductUpdate{
			"key": []byte(`"value"`),
		})
		require.Equal(t, map[string]state.ApplyStatus{
			"key": {State: state.ApplyStateUnknown},
		}, status)
		require.Equal(t, map[string]dynamicInstrumentationRCProbeConfig{
			"key": {
				configPath:    "key",
				configContent: `"value"`,
			},
		}, getDiRCState())
		status = tracer.dynamicInstrumentationRCUpdate(remoteconfig.ProductUpdate{
			"key": nil,
		})
		require.Equal(t, map[string]state.ApplyStatus{
			"key": {State: state.ApplyStateAcknowledged},
		}, status)
		require.Empty(t, getDiRCState())
	})

	t.Run("symdb updates", func(t *testing.T) {
		t.Setenv("DD_DYNAMIC_INSTRUMENTATION_ENABLED", "true")
		tracer := startTracer(t)
		startRemoteConfig(t, tracer)
		status := tracer.dynamicInstrumentationSymDBRCUpdate(remoteconfig.ProductUpdate{
			"key": []byte(`"value"`),
		})
		require.Equal(t, map[string]state.ApplyStatus{
			"key": {State: state.ApplyStateUnknown},
		}, status)
		require.Equal(t, true, getDiSymDBEnabled())
		status = tracer.dynamicInstrumentationSymDBRCUpdate(remoteconfig.ProductUpdate{
			"key": nil,
		})
		require.Equal(t, map[string]state.ApplyStatus{
			"key": {State: state.ApplyStateAcknowledged},
		}, status)
		require.Equal(t, false, getDiSymDBEnabled())
	})
}

func TestStartRemoteConfig(t *testing.T) {
	tracer, _, _, stop, err := startTestTracer(t)
	require.Nil(t, err)
	defer stop()

	tracer.startRemoteConfig(remoteconfig.DefaultClientConfig())
	defer remoteconfig.Reset()
	defer remoteconfig.Stop()

	found, err := remoteconfig.HasProduct(state.ProductAPMTracing)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingSampleRate)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingSampleRules)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingHTTPHeaderTags)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingCustomTags)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingEnabled)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingMulticonfig)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingEnableLiveDebugging)
	require.NoError(t, err)
	require.True(t, found)
}

func TestDeadLockIssue3541(t *testing.T) {
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")

	ctx, cancel := context.WithCancel(context.TODO())

	// It's not possible to use startTestTracer to reproduce the issue,
	// because it doesn't start the remote config client.
	Start(WithRuntimeMetrics(), WithTestDefaults(nil))
	defer Stop()

	go func() {
		span := StartSpan("test")

		// close the context
		cancel()
		span.Finish()
	}()

	// wait for the goroutine to finish
	<-ctx.Done()
	assert.Equal(t, context.Canceled, ctx.Err())
}

func TestRemoteConfigMulticonfig(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	floatPtr := func(f float64) *float64 { return &f }

	tests := []struct {
		name     string
		configs  []configData
		expected libConfig
	}{
		{
			name: "each field independently uses highest priority config that defines it",
			configs: []configData{
				{
					// Lowest priority (1): no target.
					ServiceTarget: target{Service: "", Env: ""},
					LibConfig: libConfig{
						SamplingRate: floatPtr(0.1),
						Enabled:      boolPtr(true),
						TraceSamplingRules: &[]rcSamplingRule{
							{Service: "no-target", Resource: "*", SampleRate: 0.1},
						},
						Tags:       &tags{"tag:val"},
						HeaderTags: &headerTags{{Header: "hdr", TagName: "tag"}},
					},
				},
				{
					// Priority 2: cluster
					K8sTargetV2: k8sTargetV2{ClusterTargets: []clusterTarget{{ClusterName: "cluster1"}}},
					LibConfig: libConfig{
						SamplingRate: floatPtr(0.2),
						HeaderTags:   &headerTags{{Header: "X-Request-Id", TagName: "request_id"}},
						TraceSamplingRules: &[]rcSamplingRule{
							{Service: "my-cluster", Resource: "*", SampleRate: 0.2},
						},
						Tags: &tags{"cluster:my-cluster"},
					},
				},
				{
					// Priority 3: env
					ServiceTarget: target{Service: "", Env: "prod"},
					LibConfig: libConfig{
						SamplingRate: floatPtr(0.3),
						TraceSamplingRules: &[]rcSamplingRule{
							{Service: "my-service-and-env", Resource: "*", SampleRate: 0.3},
						},
						Tags: &tags{"env:prod", "team:backend"},
					},
				},
				{
					// Priority 4: service
					ServiceTarget: target{Service: "my-service", Env: ""},
					LibConfig: libConfig{
						SamplingRate: floatPtr(0.4),
						TraceSamplingRules: &[]rcSamplingRule{
							{Service: "my-service", Resource: "*", SampleRate: 0.4},
						},
					},
				},
				{
					// Highest priority (5): service + env
					ServiceTarget: target{Service: "my-service", Env: "prod"},
					LibConfig: libConfig{
						SamplingRate: floatPtr(0.5),
					},
				},
			},
			expected: libConfig{
				SamplingRate: floatPtr(0.5),
				TraceSamplingRules: &[]rcSamplingRule{
					{Service: "my-service", Resource: "*", SampleRate: 0.4},
				},
				Tags:       &tags{"env:prod", "team:backend"},
				HeaderTags: &headerTags{{Header: "X-Request-Id", TagName: "request_id"}},
				Enabled:    boolPtr(true),
			},
		},
		{
			name: "wildcard service is treated as org-level",
			configs: []configData{
				{
					ServiceTarget: target{Service: "*", Env: ""},
					LibConfig:     libConfig{SamplingRate: floatPtr(0.1)},
				},
				{
					ServiceTarget: target{Service: "my-service", Env: ""},
					LibConfig:     libConfig{SamplingRate: floatPtr(0.5)},
				},
			},
			expected: libConfig{
				SamplingRate: floatPtr(0.5),
			},
		},
		{
			name: "wildcard env is treated as service-only",
			configs: []configData{
				{
					ServiceTarget: target{Service: "my-service", Env: "*"},
					LibConfig:     libConfig{SamplingRate: floatPtr(0.4)},
				},
				{
					ServiceTarget: target{Service: "my-service", Env: "prod"},
					LibConfig:     libConfig{SamplingRate: floatPtr(0.9)},
				},
			},
			expected: libConfig{
				SamplingRate: floatPtr(0.9),
			},
		},
		{
			name:     "empty configs returns empty libConfig",
			configs:  []configData{},
			expected: libConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeConfigsByPriority(tt.configs)

			if tt.expected.SamplingRate != nil {
				require.NotNil(t, result.SamplingRate)
				assert.Equal(t, *tt.expected.SamplingRate, *result.SamplingRate)
			} else {
				assert.Nil(t, result.SamplingRate)
			}

			if tt.expected.Enabled != nil {
				require.NotNil(t, result.Enabled)
				assert.Equal(t, *tt.expected.Enabled, *result.Enabled)
			} else {
				assert.Nil(t, result.Enabled)
			}

			if tt.expected.TraceSamplingRules != nil {
				require.NotNil(t, result.TraceSamplingRules)
				assert.Equal(t, *tt.expected.TraceSamplingRules, *result.TraceSamplingRules)
			} else {
				assert.Nil(t, result.TraceSamplingRules)
			}

			if tt.expected.HeaderTags != nil {
				require.NotNil(t, result.HeaderTags)
				assert.Equal(t, *tt.expected.HeaderTags, *result.HeaderTags)
			} else {
				assert.Nil(t, result.HeaderTags)
			}

			if tt.expected.Tags != nil {
				require.NotNil(t, result.Tags)
				assert.Equal(t, *tt.expected.Tags, *result.Tags)
			} else {
				assert.Nil(t, result.Tags)
			}
		})
	}
}

// Test that mergeConfigsByPriority handles all fields of libConfig.
//
// If this test fails because a new field was added to libConfig, make sure
// mergeConfigsByPriority  update the handles the new field and add the field
// name to the test.
func TestMergeHandlesAllLibConfigFields(t *testing.T) {
	handled := map[string]bool{
		"Enabled":              true,
		"SamplingRate":         true,
		"TraceSamplingRules":   true,
		"HeaderTags":           true,
		"Tags":                 true,
		"LiveDebuggingEnabled": true,
	}

	typ := reflect.TypeOf(libConfig{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i).Name
		if !handled[field] {
			t.Errorf("libConfig field %q is not handled in mergeConfigsByPriority (or at least not acknowledged in this test)", field)
		}
	}
}
