// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package samplingrules

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTelemetryStringMatchesCurrentSamplingRuleFormatting(t *testing.T) {
	rules := append(
		TraceSamplingRules(Rule{ServiceGlob: "checkout*", Rate: 0.5}),
		SpanSamplingRules(Rule{NameGlob: "http.*", Rate: 0.25, MaxPerSecond: 2})...,
	)
	want := "[" + rules[0].String() + " " + rules[1].String() + "]"

	got, err := TelemetryString(rules)

	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NotContains(t, got, `"type"`, "current SamplingRule.MarshalJSON omits rule type")
}
