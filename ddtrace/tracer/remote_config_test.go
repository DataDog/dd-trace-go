// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"

	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/require"
)

func TestOnRemoteConfigUpdate(t *testing.T) {
	t.Run("RC sampling rate = 0.5 is applied and can be reverted", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		// Apply RC. Assert _dd.rule_psr shows the RC sampling rate (0.2) is applied
		input := map[string]remoteconfig.ProductUpdate{
			"APM_TRACING": {"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.5}}`)},
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request").(*Span)
		s.Finish()
		require.Equal(t, 0.5, s.Metrics[keyRulesSamplerAppliedRate])

		// Unset RC. Assert _dd.rule_psr is not set
		input["APM_TRACING"] = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request").(*Span)
		s.Finish()
		require.NotContains(t, keyRulesSamplerAppliedRate, s.Metrics)
	})

	t.Run("DD_TRACE_SAMPLE_RATE=0.1 and RC sampling rate = 0.2", func(t *testing.T) {
		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.1")
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		// Apply RC. Assert _dd.rule_psr shows the RC sampling rate (0.2) is applied
		input := map[string]remoteconfig.ProductUpdate{
			"APM_TRACING": {"path": []byte(`{"lib_config": {"tracing_sampling_rate": 0.2}}`)},
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s := tracer.StartSpan("web.request").(*Span)
		s.Finish()
		require.Equal(t, 0.2, s.Metrics[keyRulesSamplerAppliedRate])

		// Unset RC. Assert _dd.rule_psr shows the previous sampling rate (0.1) is applied
		input["APM_TRACING"] = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		s = tracer.StartSpan("web.request").(*Span)
		s.Finish()
		require.Equal(t, 0.1, s.Metrics[keyRulesSamplerAppliedRate])
	})

	t.Run("RC header tags = X-Test-Header:my-tag-name is applied and can be reverted", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		// Apply RC. Assert global config shows the RC header tag is applied
		input := map[string]remoteconfig.ProductUpdate{
			"APM_TRACING": {"path": []byte(`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name"}]}}`)},
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name", globalconfig.HeaderTag("X-Test-Header"))

		// Unset RC. Assert header tags are not set
		input["APM_TRACING"] = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 0, globalconfig.HeaderTagsLen())
	})

	t.Run("DD_TRACE_HEADER_TAGS=X-Test-Header:my-tag-name-from-env and RC header tags = X-Test-Header:my-tag-name-from-rc", func(t *testing.T) {
		t.Setenv("DD_TRACE_HEADER_TAGS", "X-Test-Header:my-tag-name-from-env")
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		// Apply RC. Assert global config shows the RC header tag is applied
		input := map[string]remoteconfig.ProductUpdate{
			"APM_TRACING": {"path": []byte(`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name-from-rc"}]}}`)},
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-from-rc", globalconfig.HeaderTag("X-Test-Header"))

		// Unset RC. Assert global config shows the DD_TRACE_HEADER_TAGS header tag
		input["APM_TRACING"] = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-from-env", globalconfig.HeaderTag("X-Test-Header"))
	})

	t.Run("In code header tags = X-Test-Header:my-tag-name-in-code and RC header tags = X-Test-Header:my-tag-name-from-rc", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithHeaderTags([]string{"X-Test-Header:my-tag-name-in-code"}))
		defer stop()

		// Apply RC. Assert global config shows the RC header tag is applied
		input := map[string]remoteconfig.ProductUpdate{
			"APM_TRACING": {"path": []byte(`{"lib_config": {"tracing_header_tags": [{"header": "X-Test-Header", "tag_name": "my-tag-name-from-rc"}]}}`)},
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-from-rc", globalconfig.HeaderTag("X-Test-Header"))

		// Unset RC. Assert global config shows the in-code header tag
		input["APM_TRACING"] = remoteconfig.ProductUpdate{"path": []byte(`{"lib_config": {}}`)}
		applyStatus = tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateAcknowledged, applyStatus["path"].State)
		require.Equal(t, 1, globalconfig.HeaderTagsLen())
		require.Equal(t, "my-tag-name-in-code", globalconfig.HeaderTag("X-Test-Header"))
	})

	t.Run("Invalid payload", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		input := map[string]remoteconfig.ProductUpdate{
			"APM_TRACING": {"path": []byte(`{"lib_config": {"tracing_sampling_rate": "string value"}}`)},
		}
		applyStatus := tracer.onRemoteConfigUpdate(input)
		require.Equal(t, state.ApplyStateError, applyStatus["path"].State)
		require.NotEmpty(t, applyStatus["path"].Error)
	})
}
