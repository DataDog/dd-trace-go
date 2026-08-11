// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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

func TestQuarantinedRaceAttemptToFixSettingIsTotalExecutionCount(t *testing.T) {
	assert.Equal(t, 1, processRetryAttemptToFixExecutionCount(0))
	assert.Equal(t, 1, processRetryAttemptToFixExecutionCount(1))
	assert.Equal(t, 3, processRetryAttemptToFixExecutionCount(3))
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

func TestQuarantinedRaceITRDecisionExecutesModifiedTest(t *testing.T) {
	const name = "quarantinedRaceSkippableFixture"
	cfg := &processRetrySubtreeConfig{ITR: []processRetrySubtreeITR{{TestName: name}}}
	fn := runtime.FuncForPC(reflect.ValueOf(quarantinedRaceSkippableFixture).Pointer())

	skip, forced := cfg.itrDecision(name, processRetrySubtreeDirective{Modified: true}, fn)
	assert.False(t, skip)
	assert.False(t, forced)
}

func TestQuarantinedRaceUsesTestingRaceOwnership(t *testing.T) {
	baseline := &atomic.Int64{}
	logged := &atomic.Bool{}
	baseline.Store(3)
	fields := &commonPrivateFields{lastRaceErrors: baseline, raceErrorLogged: logged}

	assert.False(t, processRetrySubtreeRaceDetected(fields, 1, 3), "an advanced testing baseline must not be re-attributed")
	assert.True(t, processRetrySubtreeRaceDetected(fields, 1, 4), "race after resume must still be attributed")
	logged.Store(true)
	assert.True(t, processRetrySubtreeRaceDetected(fields, 1, 3), "a race owned by the test must remain attributed")
}

func TestOrchestrionParallelAdviceMatchesHookContract(t *testing.T) {
	source, err := os.ReadFile("orchestrion.yml")
	require.NoError(t, err)

	config := string(source)
	assert.Contains(t, config, "func __dd_civisibility_instrumentTestingParallel(t *T) (bool, func())")
	assert.Contains(t, config, "__dd_civisibility_handled, __dd_civisibility_resume := __dd_civisibility_instrumentTestingParallel")
	assert.Contains(t, config, "defer __dd_civisibility_resume()")
}

func TestQuarantinedRaceNestedRootEnvelopePreservesRecordedResult(t *testing.T) {
	for _, tt := range []struct {
		name       string
		skipped    bool
		forced     bool
		modified   bool
		controlled processRetryStatus
	}{
		{name: "skipped by ITR", skipped: true},
		{name: "forced by ITR", forced: true},
		{name: "modified", modified: true},
		{name: "ancestor controlled terminal", controlled: processRetryStatusControlledPanicReady},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &processRetrySubtreeConfig{
				Version:      processRetrySubtreeVersion,
				SelectedRoot: "TestCheckout/card",
				Root:         processRetrySubtreeDirective{TestName: "TestCheckout/card", Quarantined: true},
			}
			status := processRetryStatusPass
			skipReason := ""
			if tt.skipped {
				status = processRetryStatusSkip
				skipReason = "itr"
			}
			state := newQuarantinedRaceChildState(cfg)
			state.append(processRetrySubtreeResult{
				TestName: "TestCheckout/card", Status: status,
				Skipped: tt.skipped, SkipReason: skipReason,
				SkippedByITR: tt.skipped, ITRForcedRun: tt.forced, Modified: tt.modified,
			})
			observation := &processRetryChildObservation{
				cfg:     processRetryChildConfig{TestName: cfg.SelectedRoot, Subtree: cfg},
				subtree: state,
			}

			got := observation.buildSubtreeResult(processRetryResult{TestName: cfg.SelectedRoot}, tt.controlled)
			assert.Equal(t, status, got.Status)
			assert.False(t, got.Failed)
			assert.False(t, got.Panic)
			assert.Equal(t, tt.skipped, got.SkippedByITR)
			assert.Equal(t, tt.forced, got.ITRForcedRun)
			assert.Equal(t, tt.modified, got.Modified)
		})
	}
}

func TestQuarantinedRaceRootReplayKeepsRaceDetectorOutput(t *testing.T) {
	cfg := &processRetrySubtreeConfig{
		SelectedRoot: "TestCheckout/card",
		Root:         processRetrySubtreeDirective{TestName: "TestCheckout/card", Quarantined: true},
	}
	invocation := quarantinedRaceInvocation{
		cfg: cfg,
		attempt: processRetryAttemptResult{
			OutputTail:      "WARNING: DATA RACE\nfull detector stack",
			OutputTruncated: true,
			Result: processRetryResult{
				TestName: cfg.SelectedRoot, Status: processRetryStatusFail,
				Failed: true, OutputTail: "race detected during execution of test",
				Subtests: []processRetrySubtreeResult{{
					TestName: cfg.SelectedRoot + "/child", Status: processRetryStatusFail,
					Failed: true, RaceDetected: true,
				}},
			},
		},
	}

	got := processRetrySubtreeRootFromInvocation(invocation)
	assert.Equal(t, invocation.attempt.OutputTail, got.OutputTail)
	assert.True(t, got.OutputTruncated)
}

func TestQuarantinedRaceSubtreeOutputUsesEncodedLimit(t *testing.T) {
	for _, tt := range []struct {
		name   string
		output []byte
	}{
		{name: "escaped", output: []byte(strings.Repeat("\n", processRetrySubtreeOutputMaxBytes))},
		{name: "invalid UTF-8", output: bytes.Repeat([]byte{0xff, 'x'}, processRetrySubtreeOutputMaxBytes/2)},
		{name: "raw tail", output: []byte(strings.Repeat("x", processRetrySubtreeOutputMaxBytes+1))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := truncateProcessRetrySubtreeOutput(tt.output)
			normalized := strings.ToValidUTF8(string(tt.output), "\uFFFD")

			assert.True(t, truncated)
			assert.True(t, utf8.ValidString(got))
			assert.True(t, processRetryJSONStringFits(got, processRetrySubtreeOutputMaxBytes))
			assert.True(t, strings.HasSuffix(normalized, got))
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
