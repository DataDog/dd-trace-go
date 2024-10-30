// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"encoding/json"
	"testing"

	rules "github.com/DataDog/appsec-internal-go/appsec"
	waf "github.com/DataDog/go-libddwaf/v3"
	"github.com/stretchr/testify/require"
)

func TestStaticRule(t *testing.T) {
	if supported, _ := waf.Health(); !supported {
		t.Skip("waf disabled")
		return
	}

	var parsedRules RulesFragment
	require.NoError(t, json.Unmarshal([]byte(rules.StaticRecommendedRules), &parsedRules))
	waf, err := waf.NewHandle(parsedRules, "", "")
	require.NoError(t, err)
	require.NotNil(t, waf)
	waf.Close()
}
