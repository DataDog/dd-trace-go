// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstrumentation_AnalyticsRate(t *testing.T) {
	pkgs := GetPackages()
	for pkg, info := range pkgs {
		t.Run(string(pkg), func(t *testing.T) {
			instr := Load(pkg)

			// No env var set, without defaulting to global should return NaN
			rate := instr.AnalyticsRate(false)
			require.True(t, math.IsNaN(rate))

			// With env var set, should return 1.0
			t.Setenv("DD_TRACE_"+info.EnvVarPrefix+"_ANALYTICS_ENABLED", "true")
			rate = instr.AnalyticsRate(false)
			require.Equal(t, 1.0, rate)
		})
	}
}
