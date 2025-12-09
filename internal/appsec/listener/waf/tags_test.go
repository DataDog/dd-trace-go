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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	emitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/timer"
	"github.com/stretchr/testify/require"
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
	AddRulesMonitoringTags(&th)

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

	AddWAFMonitoringTags(&th, metrics, "1.2.3", map[libddwaf.TruncationReason][]int{
		libddwaf.ObjectTooDeep: {1, 2, 3},
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
