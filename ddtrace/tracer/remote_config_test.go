// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.Equal(t, 0.5, s.metrics[keyRulesSamplerAppliedRate])

		// Telemetry
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: 0.5, Origin: telemetry.OriginRemoteConfig})

		//Apply RC with sampling rules. Assert _dd.rule_psr shows the corresponding rule matched rate.
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
		require.Equal(t, 1.0, s.metrics[keyRulesSamplerAppliedRate])
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])
		// Spans not matching the rule still gets the global rate
		s = tracer.StartSpan("not.web.request")
		s.Finish()
		require.Equal(t, 0.5, s.metrics[keyRulesSamplerAppliedRate])
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
		require.Equal(t, 0.2, s.metrics[keyRulesSamplerAppliedRate])

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_sample_rate", Value: 0.2, Origin: telemetry.OriginRemoteConfig})

		// Unset RC. Assert _dd.rule_psr shows the previous sampling rate (0.1) is applied
		input = remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.Finish()
		require.Equal(t, 0.1, s.metrics[keyRulesSamplerAppliedRate])

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
		require.Equal(t, 0.1, s.metrics[keyRulesSamplerAppliedRate])
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
		require.Equal(t, 1.0, s.metrics[keyRulesSamplerAppliedRate])
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])
		// Spans not matching the rule gets the global rate, but not the local rule, which is no longer in effect
		s = tracer.StartSpan("web.request")
		s.resource = "not_abc"
		s.Finish()
		require.Equal(t, 0.5, s.metrics[keyRulesSamplerAppliedRate])
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

	t.Run("DD_TRACE_SAMPLING_RULES=0.1 and RC rule rate=1.0 and revert", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		defer telemetry.MockClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLING_RULES", `[{
				"service": "my-service",
				"name": "web.request",
				"resource": "*",
				"sample_rate": 0.1
			}]`)
		tracer, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		require.NoError(t, err)

		s := tracer.StartSpan("web.request")
		s.Finish()
		require.Equal(t, 0.1, s.metrics[keyRulesSamplerAppliedRate])
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
			},
			{
				"service": "my-service",
				"name": "web.request",
				"resource": "*",
				"provenance": "dynamic",
				"sample_rate": 0.3
			}]},
			"service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.resource = "abc"
		s.Finish()
		require.Equal(t, 1.0, s.metrics[keyRulesSamplerAppliedRate])
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])
		// Spans not matching the rule gets the global rate, but not the local rule, which is no longer in effect
		s = tracer.StartSpan("web.request")
		s.resource = "not_abc"
		s.Finish()
		require.Equal(t, 0.3, s.metrics[keyRulesSamplerAppliedRate])
		if p, ok := s.context.trace.samplingPriority(); ok && p > 0 {
			require.Equal(
				t,
				samplerToDM(samplernames.RemoteDynamicRule),
				s.context.trace.propagatingTags[keyDecisionMaker],
			)
		}

		// Reset restores local rules
		input = remoteconfig.ProductUpdate{"path": nil}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.resource = "not_abc"
		s.Finish()
		require.Equal(t, 0.1, s.metrics[keyRulesSamplerAppliedRate])
		if p, ok := s.context.trace.samplingPriority(); ok && p > 0 {
			require.Equal(t, samplerToDM(samplernames.RuleRate), s.context.trace.propagatingTags[keyDecisionMaker])
		}

		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_sample_rate", Value: 0.5, Origin: telemetry.OriginRemoteConfig},
			{Name: "trace_sample_rules",
				Value: `[{"service":"my-service","name":"web.request","resource":"abc","sample_rate":1,"provenance":"customer"} {"service":"my-service","name":"web.request","resource":"*","sample_rate":0.3,"provenance":"dynamic"}]`, Origin: telemetry.OriginRemoteConfig},
		})
		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_sample_rate", Value: nil, Origin: telemetry.OriginDefault},
			{
				Name:   "trace_sample_rules",
				Value:  `[{"service":"my-service","name":"web.request","resource":"*","sample_rate":0.1}]`,
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
		require.Equal(t, 1.0, s.metrics[keyRulesSamplerAppliedRate])
		require.Equal(t, samplerToDM(samplernames.RemoteUserRule), s.context.trace.propagatingTags[keyDecisionMaker])

		// A span with non-matching tags gets the global rate
		s = tracer.StartSpan("web.request")
		s.SetTag("tag-a", "not-matching")
		s.Finish()
		require.Equal(t, 0.5, s.metrics[keyRulesSamplerAppliedRate])

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
		require.NotContains(t, "key0", s.meta)
		require.NotContains(t, "key1", s.meta)
		require.NotContains(t, "key2", s.meta)
		require.Equal(t, "val3", s.meta["key3"])
		require.Equal(t, "val4", s.meta["key4"])
		require.Equal(t, globalconfig.RuntimeID(), s.meta[ext.RuntimeID])
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
		require.Equal(t, "val0", s.meta["key0"])
		require.Equal(t, "val1", s.meta["key1"])
		require.Equal(t, "val2", s.meta["key2"])
		require.NotContains(t, "key3", s.meta)
		require.NotContains(t, "key4", s.meta)
		require.Equal(t, globalconfig.RuntimeID(), s.meta[ext.RuntimeID])

		// Telemetry
		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_tags", Value: "key0:val0,key1:val1,key2:val2," + runtimeIDTag, Origin: telemetry.OriginDefault},
		},
		)
	})

	t.Run("Deleted config", func(t *testing.T) {
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
		input := remoteconfig.ProductUpdate{
			"path": []byte(
				`{"lib_config": {"tracing_sampling_rate": 0.2,"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-from-rc"}],"tracing_tags": ["ddtag:from-rc"]}, "service_target": {"service": "my-service", "env": "my-env"}}`,
			),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request")
		s.Finish()
		require.Equal(t, 0.2, s.metrics[keyRulesSamplerAppliedRate])
		require.Equal(t, "my-tag-from-rc", globalconfig.HeaderTag("X-Test-Header"))
		require.Equal(t, "from-rc", s.meta["ddtag"])

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

		// Remove RC. Assert configuration is reset to the original values.
		input = remoteconfig.ProductUpdate{"path": nil}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request")
		s.Finish()
		require.Equal(t, 0.1, s.metrics[keyRulesSamplerAppliedRate])
		require.Equal(t, "my-tag-from-env", globalconfig.HeaderTag("X-Test-Header"))
		require.Equal(t, "from-env", s.meta["ddtag"])

		// Telemetry
		assertCalled(t, telemetryClient, []telemetry.Configuration{
			{Name: "trace_sample_rate", Value: 0.1, Origin: telemetry.OriginDefault},
			{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-from-env", Origin: telemetry.OriginDefault},
			{Name: "trace_tags", Value: "ddtag:from-env," + ext.RuntimeID + ":" + globalconfig.RuntimeID(), Origin: telemetry.OriginDefault},
		})
	})

	assert.Equal(t, 0, globalconfig.HeaderTagsLen())
}

func TestStartRemoteConfig(t *testing.T) {
	tracer, _, _, stop, err := startTestTracer(t)
	require.Nil(t, err)
	defer stop()

	tracer.startRemoteConfig(remoteconfig.DefaultClientConfig())
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
