// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/remoteconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"

	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/require"
)

func TestOnRemoteConfigUpdate(t *testing.T) {
	t.Run("RC sampling rate = 0.5 is applied and can be reverted", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		// Apply RC. Assert _dd.rule_psr shows the RC sampling rate (0.2) is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.5}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request").(*span)
		s.Finish()
		require.Equal(t, 0.5, s.Metrics[keyRulesSamplerAppliedRate])

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 1)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_sample_rate", Value: 0.5, Origin: "remote_config"}})

		// Unset RC. Assert _dd.rule_psr is not set
		input = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request").(*span)
		s.Finish()
		require.NotContains(t, keyRulesSamplerAppliedRate, s.Metrics)

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 2)
		// Not calling AssertCalled because the configuration contains a math.NaN()
		// as value which cannot be asserted see https://github.com/stretchr/testify/issues/624
	})

	t.Run("DD_TRACE_SAMPLE_RATE=0.1 and RC sampling rate = 0.2", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.1")
		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		// Apply RC. Assert _dd.rule_psr shows the RC sampling rate (0.2) is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.2}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request").(*span)
		s.Finish()
		require.Equal(t, 0.2, s.Metrics[keyRulesSamplerAppliedRate])

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 1)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_sample_rate", Value: 0.2, Origin: "remote_config"}})

		// Unset RC. Assert _dd.rule_psr shows the previous sampling rate (0.1) is applied
		input = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request").(*span)
		s.Finish()
		require.Equal(t, 0.1, s.Metrics[keyRulesSamplerAppliedRate])

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 2)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_sample_rate", Value: 0.1, Origin: ""}})
	})

	t.Run("RC header tags = X-Test-Header:my-tag-name is applied and can be reverted", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		// Apply RC. Assert global config shows the RC header tag is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name"}]}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name", globalconfig.HeaderTag("X-Test-Header"))

		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 1)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name", Origin: "remote_config"}})

		// Unset RC. Assert header tags are not set
		input = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 0, globalconfig.HeaderTagsLen())

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 2)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_header_tags", Value: "", Origin: ""}})
	})

	t.Run("DD_TRACE_HEADER_TAGS=X-Test-Header:my-tag-name-from-env and RC header tags = X-Test-Header:my-tag-name-from-rc", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		t.Setenv("DD_TRACE_HEADER_TAGS", "X-Test-Header:my-tag-name-from-env")
		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		// Apply RC. Assert global config shows the RC header tag is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name-from-rc"}]}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-from-rc", globalconfig.HeaderTag("X-Test-Header"))

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 1)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-from-rc", Origin: "remote_config"}})

		// Unset RC. Assert global config shows the DD_TRACE_HEADER_TAGS header tag
		input = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-from-env", globalconfig.HeaderTag("X-Test-Header"))

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 2)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-from-env", Origin: ""}})
	})

	t.Run("In code header tags = X-Test-Header:my-tag-name-in-code and RC header tags = X-Test-Header:my-tag-name-from-rc", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"), WithHeaderTags([]string{"X-Test-Header:my-tag-name-in-code"}))
		defer stop()

		// Apply RC. Assert global config shows the RC header tag is applied
		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name-from-rc"}]}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-from-rc", globalconfig.HeaderTag("X-Test-Header"))

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 1)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-from-rc", Origin: "remote_config"}})

		// Unset RC. Assert global config shows the in-code header tag
		input = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-in-code", globalconfig.HeaderTag("X-Test-Header"))

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 2)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_header_tags", Value: "X-Test-Header:my-tag-name-in-code", Origin: ""}})
	})

	t.Run("Invalid payload", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
		defer stop()

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_sampling_rate": "string value", "service_target": {"service": "my-service", "env": "my-env"}}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateError, applyStatus["path"].State)
		require.NotEmpty(t, applyStatus["path"].Error)

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 0)
	})

	t.Run("Service mismatch", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer, _, _, stop := startTestTracer(t, WithServiceName("my-service"), WithEnv("my-env"))
		defer stop()

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "other-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateError, applyStatus["path"].State)
		require.Equal(t, "service mismatch", applyStatus["path"].Error)

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 0)
	})

	t.Run("Env mismatch", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		tracer, _, _, stop := startTestTracer(t, WithServiceName("my-service"), WithEnv("my-env"))
		defer stop()

		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "other-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateError, applyStatus["path"].State)
		require.Equal(t, "env mismatch", applyStatus["path"].Error)

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 0)
	})

	t.Run("DD_TAGS=key0:val0,key1:val1, WithGlobalTag=key2:val2 and RC tags = key3:val3,key4:val4", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		defer telemetry.MockGlobalClient(telemetryClient)()

		t.Setenv("DD_TAGS", "key0:val0,key1:val1")
		tracer, _, _, stop := startTestTracer(t, WithService("my-service"), WithEnv("my-env"), WithGlobalTag("key2", "val2"))
		defer stop()

		// Apply RC. Assert global tags have the RC tags key3:val3,key4:val4 applied + runtime ID
		input := remoteconfig.ProductUpdate{
			"path": []byte(`{"lib_config": {"tracing_tags": ["key3:val3","key4:val4"]}, "service_target": {"service": "my-service", "env": "my-env"}}`),
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request").(*span)
		s.Finish()
		require.NotContains(t, "key0", s.Meta)
		require.NotContains(t, "key1", s.Meta)
		require.NotContains(t, "key2", s.Meta)
		require.Equal(t, "val3", s.Meta["key3"])
		require.Equal(t, "val4", s.Meta["key4"])
		require.Equal(t, globalconfig.RuntimeID(), s.Meta[ext.RuntimeID])
		runtimeIDTag := ext.RuntimeID + ":" + globalconfig.RuntimeID()

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 1)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_tags", Value: "key3:val3,key4:val4," + runtimeIDTag, Origin: "remote_config"}})

		// Unset RC. Assert config shows the original DD_TAGS + WithGlobalTag + runtime ID
		input = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}, "service_target": {"service": "my-service", "env": "my-env"}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request").(*span)
		s.Finish()
		require.Equal(t, "val0", s.Meta["key0"])
		require.Equal(t, "val1", s.Meta["key1"])
		require.Equal(t, "val2", s.Meta["key2"])
		require.NotContains(t, "key3", s.Meta)
		require.NotContains(t, "key4", s.Meta)
		require.Equal(t, globalconfig.RuntimeID(), s.Meta[ext.RuntimeID])

		// Telemetry
		telemetryClient.AssertNumberOfCalls(t, "ConfigChange", 2)
		telemetryClient.AssertCalled(t, "ConfigChange", []telemetry.Configuration{{Name: "trace_tags", Value: "key0:val0,key1:val1,key2:val2," + runtimeIDTag, Origin: ""}})
	})
}

func TestStartRemoteConfig(t *testing.T) {
	tracer, _, _, stop := startTestTracer(t)
	defer stop()

	tracer.startRemoteConfig(remoteconfig.DefaultClientConfig())
	found, err := remoteconfig.HasProduct(state.ProductAPMTracing)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingSampleRate)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingHTTPHeaderTags)
	require.NoError(t, err)
	require.True(t, found)

	found, err = remoteconfig.HasCapability(remoteconfig.APMTracingCustomTags)
	require.NoError(t, err)
	require.True(t, found)
}
