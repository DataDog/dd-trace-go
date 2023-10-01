// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"encoding/json"
	"testing"

	waf "github.com/DataDog/go-libddwaf"
	"github.com/stretchr/testify/require"
)

func TestStaticRule(t *testing.T) {
	if supported, _ := waf.SupportsTarget(); !supported {
		t.Skip("waf disabled")
		return
	}

	var rules rulesFragment
	require.NoError(t, json.Unmarshal([]byte(staticRecommendedRules), &rules))
	waf, err := waf.NewHandle(rules, "", "")
	require.NoError(t, err)
	require.NotNil(t, waf)
	waf.Close()
}
