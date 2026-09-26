// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanSkipEmptyTestSession(t *testing.T) {
	m := new(testing.M)
	require.True(t, canSkipEmptyTestSession(m))
	examples := getInternalExampleArray(m)
	require.NotNil(t, examples)
	*examples = []testing.InternalExample{{Name: "ExampleSynthetic"}}
	require.False(t, canSkipEmptyTestSession(m))
	*examples = nil
	fuzzTargets := getInternalFuzzTargetArray(m)
	require.NotNil(t, fuzzTargets)
	*fuzzTargets = []testing.InternalFuzzTarget{{Name: "FuzzSynthetic"}}
	require.False(t, canSkipEmptyTestSession(m))
	require.False(t, canSkipEmptyTestSession(nil))
}
