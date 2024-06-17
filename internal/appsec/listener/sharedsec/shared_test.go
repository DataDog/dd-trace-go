// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sharedsec

import (
	"testing"

	waf "github.com/DataDog/go-libddwaf/v3"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

const (
	wafDurationTag    = "_dd.appsec.waf.duration"
	wafDurationExtTag = "_dd.appsec.waf.duration_ext"
	wafTimeoutTag     = "_dd.appsec.waf.timeouts"
)

// Test that internal functions used to set span tags use the correct types
func TestTagsTypes(t *testing.T) {
	th := trace.NewTagsHolder()
	wafDiags := waf.Diagnostics{
		Version: "1.3.0",
		Rules: &waf.DiagnosticEntry{
			Loaded: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"},
			Failed: []string{"1337"},
			Errors: map[string][]string{"test": {"1", "2"}},
		},
	}

	AddRulesMonitoringTags(&th, &wafDiags)

	stats := map[string]any{
		wafDurationTag:                     10,
		wafDurationExtTag:                  20,
		wafTimeoutTag:                      0,
		"_dd.appsec.waf.truncations.depth": []int{1, 2, 3},
		"_dd.appsec.waf.run":               12000,
	}

	AddWAFMonitoringTags(&th, "1.2.3", stats)

	tags := th.Tags()
	_, ok := tags[eventRulesErrorsTag].(string)
	require.True(t, ok)

	for _, tag := range []string{eventRulesLoadedTag, eventRulesFailedTag, wafDurationTag, wafDurationExtTag, wafVersionTag, wafTimeoutTag} {
		require.Contains(t, tags, tag)
	}
}
