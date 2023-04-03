// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"testing"

	waf "github.com/DataDog/go-libddwaf"
	"github.com/stretchr/testify/require"
)

func TestStaticRule(t *testing.T) {
	if waf.Health() != nil {
		t.Skip("waf disabled")
		return
	}
	waf, err := waf.NewHandle([]byte(staticRecommendedRules), "", "")
	require.NoError(t, err)
	waf.Close()
}
