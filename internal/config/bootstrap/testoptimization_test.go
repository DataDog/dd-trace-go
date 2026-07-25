// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTestOptimizationCachesFirstReadAndPreservesInvalidPayloads(t *testing.T) {
	ResetTestOptimizationForTesting()
	t.Cleanup(ResetTestOptimizationForTesting)
	t.Setenv(testOptimizationManifestKey, " manifest.txt ")
	t.Setenv(testOptimizationPayloadsKey, "invalid")

	first := TestOptimization()
	t.Setenv(testOptimizationManifestKey, "changed.txt")
	t.Setenv(testOptimizationPayloadsKey, "true")
	second := TestOptimization()

	require.Equal(t, "manifest.txt", first.ManifestFile)
	require.False(t, first.PayloadsInFiles)
	require.Error(t, first.PayloadsError)
	require.Equal(t, first, second)
}

func TestClaimTestOptimizationTelemetryIsSingleUseAndResettable(t *testing.T) {
	ResetTestOptimizationForTesting()
	t.Cleanup(ResetTestOptimizationForTesting)

	_, first := ClaimTestOptimizationTelemetry()
	_, second := ClaimTestOptimizationTelemetry()
	ResetTestOptimizationForTesting()
	_, third := ClaimTestOptimizationTelemetry()

	require.True(t, first)
	require.False(t, second)
	require.True(t, third)
}
