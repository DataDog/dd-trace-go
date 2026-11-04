//go:build race

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessRetryParityProcessChildRaceIsStructured(t *testing.T) {
	result, exitCode, output := runProcessRetryChildResultFixture(t, "race")
	require.NotZero(t, exitCode, output)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
	require.True(t, result.RaceDetected)
	require.Equal(t, "test_race", effectiveProcessRetryStatus(processRetryAttemptResult{
		Result:   result,
		ExitCode: exitCode,
	}, false).FailureKind)
}
