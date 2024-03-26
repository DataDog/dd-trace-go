// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sharedsec

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/appsec-internal-go/appsec"
	waf "github.com/DataDog/go-libddwaf/v2"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
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
	AddWAFMonitoringTags(&th, "1.2.3", 2, 1, 3)

	tags := th.Tags()
	_, ok := tags[eventRulesErrorsTag].(string)
	require.True(t, ok)

	for _, tag := range []string{eventRulesLoadedTag, eventRulesFailedTag, wafDurationTag, wafDurationExtTag, wafVersionTag} {
		require.Contains(t, tags, tag)
	}
}

func TestTelemetryMetrics(t *testing.T) {
	if ok, _ := waf.Health(); !ok {
		// The WAF is not available, so this test is not relevant
		t.SkipNow()
	}

	rules, err := appsec.DefaultRulesetMap()
	require.NoError(t, err)
	handle, err := waf.NewHandle(rules, appsec.DefaultObfuscatorKeyRegex, appsec.DefaultObfuscatorValueRegex)
	require.NoError(t, err)
	defer handle.Close()

	wafCtx := waf.NewContext(handle)
	defer wafCtx.Close()

	holder := trace.NewTagsHolder()
	_ = RunWAF(wafCtx, waf.RunAddressData{
		Ephemeral: map[string]any{
			"my.large.string": strings.Repeat("a", 8_192),
			"my.large.list":   make([]bool, 4_096),
			"my.deep.object":  makeDeep(30),
		},
	}, time.Minute, &holder)

	tags := holder.Tags()

	require.Equal(t, map[string]any{
		"_dd.appsec.waf.input_truncated": int(waf.StringTooLong | waf.ObjectTooDeep | waf.ContainerTooLarge),
	}, tags)
}

func makeDeep(depth int) map[string]any {
	if depth <= 0 {
		return nil
	}
	return map[string]any{
		fmt.Sprintf("depth:%d", depth): makeDeep(depth - 1),
	}
}
