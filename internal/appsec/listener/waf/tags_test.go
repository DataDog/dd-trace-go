// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"testing"
	"time"

	waf "github.com/DataDog/go-libddwaf/v3"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	emitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
)

const (
	wafDurationTag    = "_dd.appsec.waf.duration"
	wafDurationExtTag = "_dd.appsec.waf.duration_ext"
)

// Test that internal functions used to set span tags use the correct types
func TestTagsTypes(t *testing.T) {
	th := make(trace.TestTagSetter)
	wafDiags := waf.Diagnostics{
		Version: "1.3.0",
		Rules: &waf.DiagnosticEntry{
			Loaded: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"},
			Failed: []string{"1337"},
			Errors: map[string][]string{"test": {"1", "2"}},
		},
	}

	AddRulesMonitoringTags(&th, wafDiags)

	AddWAFMonitoringTags(&th, &emitter.ContextMetrics{}, "1.2.3", waf.Stats{
		Timers: map[string]time.Duration{
			"waf.duration":      10 * time.Microsecond,
			"rasp.duration":     10 * time.Microsecond,
			"waf.duration_ext":  20 * time.Microsecond,
			"rasp.duration_ext": 20 * time.Microsecond,
		},
		TimeoutCount:     0,
		TimeoutRASPCount: 2,
		Truncations: map[waf.TruncationReason][]int{
			waf.ObjectTooDeep: {1, 2, 3},
		},
	})

	tags := th.Tags()
	_, ok := tags[eventRulesErrorsTag].(string)
	require.True(t, ok)

	for _, tag := range []string{eventRulesLoadedTag, eventRulesFailedTag, wafDurationTag, wafDurationExtTag, wafVersionTag, raspTimeoutTag, truncationTagPrefix + string(waf.ObjectTooDeep)} {
		require.Contains(t, tags, tag)
	}
}
