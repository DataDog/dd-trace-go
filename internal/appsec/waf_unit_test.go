// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !noappsec

package appsec

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"

	waf "github.com/DataDog/go-libddwaf"
	"github.com/stretchr/testify/require"
)

// Test that internal functions used to set span tags use the correct types
func TestTagsTypes(t *testing.T) {
	th := instrumentation.NewTagsHolder()
	rInfo := waf.RulesetInfo{
		Version: "1.3.0",
		Loaded:  10,
		Failed:  1,
		Errors:  map[string]interface{}{"test": []string{"1", "2"}},
	}

	addRulesMonitoringTags(&th, rInfo)
	addWAFMonitoringTags(&th, "1.2.3", 2, 1, 3)

	tags := th.Tags()
	_, ok := tags[eventRulesErrorsTag].(string)
	require.True(t, ok)

	for _, tag := range []string{eventRulesLoadedTag, eventRulesFailedTag, wafDurationTag, wafDurationExtTag, wafVersionTag} {
		require.Contains(t, tags, tag)
	}
}
