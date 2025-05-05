// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	emitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/go-libddwaf/v4"
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
	AddWAFMonitoringTags(&th, &emitter.ContextMetrics{}, "1.2.3", libddwaf.Stats{
		Timers: map[string]time.Duration{
			"waf.duration":      10 * time.Microsecond,
			"rasp.duration":     10 * time.Microsecond,
			"waf.duration_ext":  20 * time.Microsecond,
			"rasp.duration_ext": 20 * time.Microsecond,
		},
		TimeoutCount:     0,
		TimeoutRASPCount: 2,
		Truncations: map[libddwaf.TruncationReason][]int{
			libddwaf.ObjectTooDeep: {1, 2, 3},
		},
	})

	tags := th.Tags()

	var expectedTags = []string{
		eventRulesVersionTag,
		wafDurationTag,
		wafDurationExtTag,
		raspDurationTag,
		raspDurationExtTag,
		wafVersionTag,
		raspTimeoutTag,
		truncationTagPrefix + libddwaf.ObjectTooDeep.String(),
		ext.ManualKeep,
	}

	slices.Sort(expectedTags)

	require.Equal(t, expectedTags, slices.Sorted(maps.Keys(tags)))
}
