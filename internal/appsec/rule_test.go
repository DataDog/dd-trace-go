// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/waf"
)

func TestStaticRule(t *testing.T) {
	if _, err := waf.Health(); err != nil {
		t.Skip("waf disabled")
		return
	}
	waf, err := waf.NewHandle([]byte(staticRecommendedRule))
	require.NoError(t, err)
	waf.Close()
}

func TestUsage(t *testing.T) {
	handle, err := waf.NewHandle([]byte(staticRecommendedRule))
	require.NoError(t, err)
	require.NotNil(t, handle)

	defer handle.Close()

	wafCtx := waf.NewContext(handle)
	require.NotNil(t, wafCtx)
	defer wafCtx.Close()

	// Matching
	// Note a WAF rule can only match once. This is why we test the matching case at the end.
	values := map[string]interface{}{
		"server.request.query": map[string]string{"value": "0000012345"},
		//"server.request.uri.raw": "/../../../secret.txt",
		//"server.response.status": "404",
		//"server.request.headers.no_cookies": map[string]string{"user-agent": "Arachni/v1"},
	}
	matches, _ := wafCtx.Run(values, time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, matches)
}
