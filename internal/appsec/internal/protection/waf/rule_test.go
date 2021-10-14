// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaticRule(t *testing.T) {
	if _, err := Health(); err != nil {
		t.Skip("waf disabled")
		return
	}
	waf, err := newWAFHandle([]byte(staticRecommendedRule))
	require.NoError(t, err)
	waf.close()
}
