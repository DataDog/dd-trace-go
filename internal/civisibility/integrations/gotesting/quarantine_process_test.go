// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quarantinedRaceSkippableFixture(*testing.T) {}

func TestQuarantinedRaceExactRunPatternAnchorsEveryComponent(t *testing.T) {
	assert.Equal(t, `^TestCheckout$/^card\[1\]$/^visa\+debit$`, processRetryExactRunPattern("TestCheckout/card[1]/visa+debit"))
	assert.Equal(t, "TestCheckout/.*", processRetryChildRunPattern("TestCheckout/.*", "TestCheckout/card"))
}

func TestQuarantinedRaceDirectiveResolutionUsesNearestAttemptOwner(t *testing.T) {
	cfg := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout/card",
		Root: processRetrySubtreeDirective{
			TestName:    "TestCheckout/card",
			Quarantined: true,
		},
		Directives: []processRetrySubtreeDirective{
			{TestName: "TestCheckout/card/visa", Quarantined: true, AttemptToFix: true},
			{TestName: "TestCheckout/card/visa/credit", Quarantined: true},
		},
	}

	directive, owner := cfg.resolveDirective("TestCheckout/card/visa")
	require.True(t, directive.AttemptToFix)
	assert.Equal(t, "TestCheckout/card/visa", owner)

	directive, owner = cfg.resolveDirective("TestCheckout/card/visa/debit")
	require.True(t, directive.AttemptToFix)
	assert.Equal(t, "TestCheckout/card/visa", owner)

	directive, owner = cfg.resolveDirective("TestCheckout/card/visa/credit")
	assert.False(t, directive.AttemptToFix)
	assert.Empty(t, owner)
}

func TestQuarantinedRaceITRDecisionPreservesSourceUnskippable(t *testing.T) {
	for _, tt := range []struct {
		name   string
		fn     func(*testing.T)
		forced bool
	}{
		{name: "TestNormalPassingAfterRetryAlwaysFail", fn: TestNormalPassingAfterRetryAlwaysFail, forced: true},
		{name: "quarantinedRaceSkippableFixture", fn: quarantinedRaceSkippableFixture},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &processRetrySubtreeConfig{ITR: []processRetrySubtreeITR{{TestName: tt.name}}}
			fn := runtime.FuncForPC(reflect.ValueOf(tt.fn).Pointer())
			skip, forced := cfg.itrDecision(tt.name, processRetrySubtreeDirective{}, fn)
			assert.Equal(t, !tt.forced, skip)
			assert.Equal(t, tt.forced, forced)
		})
	}
}

func TestQuarantinedRaceSubtreeConfigRejectsUntrustedDirectives(t *testing.T) {
	valid := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout/card",
		Root: processRetrySubtreeDirective{
			TestName:    "TestCheckout/card",
			Quarantined: true,
		},
		Directives: []processRetrySubtreeDirective{{TestName: "TestCheckout/card/visa", Quarantined: true}},
	}
	require.NoError(t, validateProcessRetrySubtreeConfig(valid, valid.SelectedRoot))

	outside := *valid
	outside.Directives = []processRetrySubtreeDirective{{TestName: "TestCheckout/paypal", Quarantined: true}}
	require.Error(t, validateProcessRetrySubtreeConfig(&outside, outside.SelectedRoot))

	duplicate := *valid
	duplicate.Directives = append(append([]processRetrySubtreeDirective(nil), valid.Directives...), valid.Directives[0])
	require.Error(t, validateProcessRetrySubtreeConfig(&duplicate, duplicate.SelectedRoot))

	mismatched := *valid
	mismatched.SelectedRoot = "TestCheckout/other"
	require.Error(t, validateProcessRetrySubtreeConfig(&mismatched, valid.SelectedRoot))
}

func TestQuarantinedRaceSubtreeResultValidationIsFailClosed(t *testing.T) {
	cfg := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout/card",
		Root: processRetrySubtreeDirective{
			TestName:    "TestCheckout/card",
			Quarantined: true,
		},
	}
	now := time.Now().UnixNano()
	valid := processRetryResult{
		Version:           1,
		TestName:          cfg.SelectedRoot,
		Attempt:           1,
		RetryReason:       processRetrySubtreeReason,
		MRunEpoch:         1,
		InvocationOrdinal: 1,
		Status:            processRetryStatusPass,
		StartUnixNano:     now,
		FinishUnixNano:    now + 10,
		DurationNanos:     10,
		Subtests: []processRetrySubtreeResult{{
			TestName:       "TestCheckout/card/visa",
			Status:         processRetryStatusPass,
			StartUnixNano:  now + 1,
			FinishUnixNano: now + 9,
			DurationNanos:  8,
			Quarantined:    true,
		}},
	}
	expected := processRetryChildConfig{
		TestName: cfg.SelectedRoot, Attempt: 1, RetryReason: processRetrySubtreeReason,
		MRunEpoch: 1, InvocationOrdinal: 1, Subtree: cfg,
	}
	require.NoError(t, validateProcessRetryResult(valid, expected))

	duplicate := valid
	duplicate.Subtests = append(append([]processRetrySubtreeResult(nil), valid.Subtests...), valid.Subtests[0])
	require.Error(t, validateProcessRetryResult(duplicate, expected))

	outside := valid
	outside.Subtests = []processRetrySubtreeResult{{
		TestName: "TestCheckout/paypal", Status: processRetryStatusPass,
		StartUnixNano: now + 1, FinishUnixNano: now + 9, DurationNanos: 8,
	}}
	require.Error(t, validateProcessRetryResult(outside, expected))

	inconsistent := valid
	inconsistent.Subtests[0].Failed = true
	require.Error(t, validateProcessRetryResult(inconsistent, expected))
}

func TestQuarantinedRaceControlConfigCarriesValidatedSnapshot(t *testing.T) {
	subtree := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout/card",
		Root:         processRetrySubtreeDirective{TestName: "TestCheckout/card", Quarantined: true},
	}
	cfg := processRetryControlConfig{
		Version: processRetryControlVersion, Transport: processRetryControlTransportUnixPipes,
		TestName: subtree.SelectedRoot, Attempt: 1, RetryReason: processRetrySubtreeReason,
		MRunEpoch: 1, InvocationOrdinal: 1, ReadEndpoint: 3, WriteEndpoint: 4,
		ObservedGOMAXPROCS: 1, Subtree: subtree,
	}
	path := filepath.Join(t.TempDir(), "control.json")
	require.NoError(t, writeProcessRetryControlConfig(path, cfg))
	got, err := readProcessRetryControlConfig(path, processRetryChildConfig{
		TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
		MRunEpoch: cfg.MRunEpoch, InvocationOrdinal: cfg.InvocationOrdinal, Subtree: subtree,
	})
	require.NoError(t, err)
	assert.Equal(t, subtree, got.Subtree)
}

func TestQuarantinedRaceAcceptsOnlyExplainedTestFailures(t *testing.T) {
	validRace := processRetryAttemptResult{
		ExitCode: 1, ExitStatusObserved: true,
		Result: processRetryResult{Status: processRetryStatusFail, Failed: true, RaceDetected: true},
	}
	assert.False(t, processRetryInfrastructureFailure(validRace))

	missing := processRetryAttemptResult{ExitCode: 0, ExitStatusObserved: true}
	assert.True(t, processRetryInfrastructureFailure(missing))

	inconsistent := processRetryAttemptResult{
		ExitCode: 1, ExitStatusObserved: true,
		Result: processRetryResult{Status: processRetryStatusPass},
	}
	assert.True(t, processRetryInfrastructureFailure(inconsistent))
}

func TestQuarantinedRaceReplayMarksTruncatedOutput(t *testing.T) {
	attempt := processRetryAttemptFromSubtreeResult(processRetrySubtreeResult{
		Status: processRetryStatusPass, OutputTail: "tail", OutputTruncated: true,
	})
	assert.Equal(t, processRetryOutputTruncationMarker+"tail", attempt.OutputTail)
}

func TestQuarantinedRaceProcessContextKeepsFailClosedSetup(t *testing.T) {
	ctx := newQuarantinedRaceProcessContext(nil, 1, &atomic.Uint64{}, 0)
	require.NotNil(t, ctx)
	assert.Nil(t, ctx.launchTemplate)
}
