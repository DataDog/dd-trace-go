// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/timer"
	"github.com/DataDog/go-libddwaf/v5/waferrors"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	emitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

const (
	wafDurationTag     = "_dd.appsec.waf.duration"
	wafDurationExtTag  = "_dd.appsec.waf.duration_ext"
	raspDurationTag    = "_dd.appsec.rasp.duration"
	raspDurationExtTag = "_dd.appsec.rasp.duration_ext"
)

// Test that internal functions used to set span tags use the correct types
func TestTagsTypes(t *testing.T) {
	th := make(trace.TestTagSetter)
	AddRulesMonitoringTags(&th, "")

	metrics := &emitter.ContextMetrics{
		SumDurations: map[addresses.Scope]map[timer.Key]*atomic.Int64{
			addresses.WAFScope:  {libddwaf.DurationTimeKey: &atomic.Int64{}},
			addresses.RASPScope: {libddwaf.DurationTimeKey: &atomic.Int64{}},
		},
	}
	metrics.SumDurations[addresses.WAFScope][libddwaf.DurationTimeKey].Store(int64(time.Millisecond))
	metrics.SumDurations[addresses.RASPScope][libddwaf.DurationTimeKey].Store(int64(time.Millisecond))
	metrics.SumWAFTimeouts.Store(1)
	metrics.SumRASPTimeouts[addresses.RASPRuleTypeLFI].Store(2)

	AddWAFMonitoringTags(&th, metrics, "1.2.3", libddwaf.Truncations{
		ObjectTooDeep: []int{1, 2, 3},
	}, map[timer.Key]time.Duration{
		addresses.WAFScope:  10 * time.Millisecond,
		addresses.RASPScope: 10 * time.Millisecond,
	})

	tags := th.Tags()

	var expectedTags = []string{
		eventRulesVersionTag,
		wafDurationTag,
		wafDurationExtTag,
		raspDurationTag,
		raspDurationExtTag,
		wafVersionTag,
		wafTimeoutTag,
		raspTimeoutTag,
		truncationTagPrefix + libddwaf.ObjectTooDeep.String(),
		ext.ManualKeep,
	}

	slices.Sort(expectedTags)

	require.Equal(t, expectedTags, slices.Sorted(maps.Keys(tags)))
}

func TestAddRulesMonitoringTagsRCClientID(t *testing.T) {
	t.Run("with_client_id", func(t *testing.T) {
		th := make(trace.TestTagSetter)
		AddRulesMonitoringTags(&th, "test-client-id")
		require.Equal(t, "test-client-id", th.Tags()["_dd.rc.client_id"])
	})
	t.Run("without_client_id", func(t *testing.T) {
		th := make(trace.TestTagSetter)
		AddRulesMonitoringTags(&th, "")
		_, ok := th.Tags()["_dd.rc.client_id"]
		require.False(t, ok)
	})
}

func TestAddWAFMonitoringTags_includes_provided_stats(t *testing.T) {
	th := make(trace.TestTagSetter)
	handleMetrics := emitter.NewMetricsInstance(nil, "1.2.3")
	metrics := handleMetrics.NewContextMetrics()

	truncations := libddwaf.Truncations{
		StringTooLong: []int{42},
	}
	timerStats := map[timer.Key]time.Duration{
		addresses.RASPScope: 2 * time.Millisecond,
	}

	AddWAFMonitoringTags(&th, metrics, "1.2.3", truncations, timerStats)

	tags := th.Tags()
	require.Equal(t, float64(2000), tags[raspDurationExtTag])
	require.Equal(t, 42, tags[truncationTagPrefix+libddwaf.StringTooLong.String()])
}

func TestWAFErrorCodeSpanTag(t *testing.T) {
	t.Run("waf_error_emits_code_not_count", func(t *testing.T) {
		th := make(trace.TestTagSetter)
		metrics := &emitter.ContextMetrics{
			SumDurations: map[addresses.Scope]map[timer.Key]*atomic.Int64{
				addresses.WAFScope:  {libddwaf.DurationTimeKey: &atomic.Int64{}},
				addresses.RASPScope: {libddwaf.DurationTimeKey: &atomic.Int64{}},
			},
		}
		metrics.WAFErrorCode.Store(-2)
		AddWAFMonitoringTags(&th, metrics, "1.0.0", libddwaf.Truncations{}, map[timer.Key]time.Duration{
			addresses.WAFScope:  time.Millisecond,
			addresses.RASPScope: time.Millisecond,
		})
		require.Equal(t, int32(-2), th.Tags()[wafErrorTag])
	})

	t.Run("no_waf_error_no_tag", func(t *testing.T) {
		th := make(trace.TestTagSetter)
		metrics := &emitter.ContextMetrics{
			SumDurations: map[addresses.Scope]map[timer.Key]*atomic.Int64{
				addresses.WAFScope:  {libddwaf.DurationTimeKey: &atomic.Int64{}},
				addresses.RASPScope: {libddwaf.DurationTimeKey: &atomic.Int64{}},
			},
		}
		AddWAFMonitoringTags(&th, metrics, "1.0.0", libddwaf.Truncations{}, map[timer.Key]time.Duration{
			addresses.WAFScope:  time.Millisecond,
			addresses.RASPScope: time.Millisecond,
		})
		_, ok := th.Tags()[wafErrorTag]
		require.False(t, ok)
	})

	t.Run("rasp_error_closest_to_zero_across_rule_types", func(t *testing.T) {
		th := make(trace.TestTagSetter)
		metrics := &emitter.ContextMetrics{
			SumDurations: map[addresses.Scope]map[timer.Key]*atomic.Int64{
				addresses.WAFScope:  {libddwaf.DurationTimeKey: &atomic.Int64{}},
				addresses.RASPScope: {libddwaf.DurationTimeKey: &atomic.Int64{}},
			},
		}
		metrics.RASPErrorCodes[addresses.RASPRuleTypeLFI].Store(-127)
		metrics.RASPErrorCodes[addresses.RASPRuleTypeSQLI].Store(-2)
		metrics.RASPErrorCodes[addresses.RASPRuleTypeCMDI].Store(-1)
		AddWAFMonitoringTags(&th, metrics, "1.0.0", libddwaf.Truncations{}, map[timer.Key]time.Duration{
			addresses.WAFScope:  time.Millisecond,
			addresses.RASPScope: time.Millisecond,
		})
		// closest-to-zero across all rule types: -1
		require.Equal(t, int32(-1), th.Tags()[raspErrorTag])
	})

	// The subtests below do not call t.Parallel: they replace the global
	// telemetry client.
	wafAddrs := func() addresses.RunAddressData {
		return addresses.RunAddressData{TimerKey: addresses.WAFScope}
	}
	raspAddrs := func() addresses.RunAddressData {
		return addresses.NewAddressesBuilder().WithDBStatement("SELECT 1").WithDBType("postgresql").Build()
	}

	// tagsAfter returns the span tags that AddWAFMonitoringTags sets after
	// IncWafError received each error, in order.
	tagsAfter := func(t *testing.T, addrs func() addresses.RunAddressData, errs ...error) map[string]any {
		t.Helper()
		defer telemetry.MockClient(new(telemetrytest.RecordClient))()
		handleMetrics := emitter.NewMetricsInstance(nil, "test")
		metrics := handleMetrics.NewContextMetrics()
		for _, err := range errs {
			metrics.IncWafError(addrs(), err)
		}
		th := make(trace.TestTagSetter)
		AddWAFMonitoringTags(&th, metrics, "1.0.0", libddwaf.Truncations{}, map[timer.Key]time.Duration{
			addresses.WAFScope:  time.Millisecond,
			addresses.RASPScope: time.Millisecond,
		})
		return th.Tags()
	}

	t.Run("waf_timeout_sets_no_error_tag", func(t *testing.T) {
		tags := tagsAfter(t, wafAddrs, waferrors.ErrTimeout)
		require.NotContains(t, tags, wafErrorTag)
		require.NotContains(t, tags, raspErrorTag)
	})

	t.Run("rasp_timeout_sets_no_error_tag", func(t *testing.T) {
		tags := tagsAfter(t, raspAddrs, waferrors.ErrTimeout)
		require.NotContains(t, tags, wafErrorTag)
		require.NotContains(t, tags, raspErrorTag)
	})

	for _, order := range []struct {
		name string
		errs []error
	}{
		{name: "error_then_timeout", errs: []error{waferrors.ErrInternal, waferrors.ErrTimeout}},
		{name: "timeout_then_error", errs: []error{waferrors.ErrTimeout, waferrors.ErrInternal}},
	} {
		t.Run("waf_timeout_does_not_hide_real_error/"+order.name, func(t *testing.T) {
			tags := tagsAfter(t, wafAddrs, order.errs...)
			require.Equal(t, int32(waferrors.ErrInternal), tags[wafErrorTag])
		})

		t.Run("rasp_timeout_does_not_hide_real_error/"+order.name, func(t *testing.T) {
			tags := tagsAfter(t, raspAddrs, order.errs...)
			require.Equal(t, int32(waferrors.ErrInternal), tags[raspErrorTag])
		})
	}
}
