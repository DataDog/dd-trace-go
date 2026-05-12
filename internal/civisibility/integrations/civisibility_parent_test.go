// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internalenv "github.com/DataDog/dd-trace-go/v2/internal/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalCiVisibilityInitializationParentModeRewritesEnvAfterTracerInitialization(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	disableAdditionalFeaturesForBootstrapTest()
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")

	var valueDuringTracerInit string
	internalCiVisibilityInitialization(func(_ []tracer.StartOption) {
		var ok bool
		valueDuringTracerInit, ok = internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
		require.True(t, ok)
	})

	valueAfterTracerInit, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	require.True(t, ok)
	assert.Equal(t, "1", valueDuringTracerInit)
	assert.Equal(t, "false", valueAfterTracerInit)
}

func TestInternalCiVisibilityInitializationExplicitTrueDoesNotRewriteEnvToFalse(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	disableAdditionalFeaturesForBootstrapTest()
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "true")

	var valueDuringTracerInit string
	internalCiVisibilityInitialization(func(_ []tracer.StartOption) {
		var ok bool
		valueDuringTracerInit, ok = internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
		require.True(t, ok)
	})

	valueAfterTracerInit, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	require.True(t, ok)
	assert.Equal(t, "1", valueDuringTracerInit)
	assert.Equal(t, "1", valueAfterTracerInit)
}

// resetCIVisibilityBootstrapStateForTesting clears bootstrap-only global state while preserving package test mode.
func resetCIVisibilityBootstrapStateForTesting() {
	resetCIVisibilityStateForTesting()
	testMode := civisibility.IsTestMode()
	civisibility.ResetForTesting()
	if testMode {
		civisibility.SetTestMode()
	}
	ciVisibilityInitializationOnce = sync.Once{}
	mTracer = nil
}

// restoreCIVisibilityMockModeForTesting restores the package-level mock mode that TestMain installed.
func restoreCIVisibilityMockModeForTesting() {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	// Stop the existing mock before starting another one so its Stop hook cannot
	// overwrite the newly installed global tracer with the noop tracer.
	tracer.Stop()
	mockTracer = InitializeCIVisibilityMock()
}

// disableAdditionalFeaturesForBootstrapTest prevents bootstrap tests from starting asynchronous backend setup.
func disableAdditionalFeaturesForBootstrapTest() {
	additionalFeaturesInitializationOnce = sync.Once{}
	additionalFeaturesInitializationOnce.Do(func() {})
}
