// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
)

// Test that internal functions used to set span tags use the correct types
func TestTagsTypes(t *testing.T) {
	const (
		eventRulesErrorsTag = "_dd.appsec.event_rules.errors"
		eventRulesLoadedTag = "_dd.appsec.event_rules.loaded"
		eventRulesFailedTag = "_dd.appsec.event_rules.error_count"
		wafDurationTag      = "_dd.appsec.waf.duration"
		wafDurationExtTag   = "_dd.appsec.waf.duration_ext"
	)

	th := instrumentation.NewTagsHolder()
	rInfo := waf.RulesetInfo{
		Version: "1.3.0",
		Loaded:  10,
		Failed:  1,
		Errors:  map[string]interface{}{"test": []string{"1", "2"}},
	}

	addRulesetInfoTags(&th, rInfo)
	addWAFDurationTags(&th, 1.0, 2.0)

	tags := th.Tags()
	_, ok := tags[eventRulesErrorsTag].(string)
	require.True(t, ok)

	for _, tag := range []string{eventRulesLoadedTag, eventRulesFailedTag, wafDurationTag, wafDurationExtTag} {
		_, ok := tags[tag].(float64)
		require.True(t, ok)
	}
}
