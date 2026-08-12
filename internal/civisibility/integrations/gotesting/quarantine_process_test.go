// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
)

func quarantinedRaceSkippableFixture(*testing.T) {}

func TestQuarantinedRaceExactRunPatternAnchorsEveryComponent(t *testing.T) {
	assert.Equal(t, `^TestCheckout$/^card\[1\]$/^visa\+debit$`, processRetryExactRunPattern("TestCheckout/card[1]/visa+debit"))
	assert.Equal(t, "TestCheckout/.*", processRetryChildRunPattern("TestCheckout/.*", "TestCheckout/card"))
}

func TestQuarantinedRaceCoverageCoordinatorDiscardsOnlySiblingOverlap(t *testing.T) {
	var coordinator quarantinedRaceCoverageCoordinator
	parent := coordinator.begin("TestCheckout/card")
	child := coordinator.begin("TestCheckout/card/visa")
	assert.True(t, coordinator.finish(child))
	assert.True(t, coordinator.finish(parent))

	first := coordinator.begin("TestCheckout/card/visa")
	second := coordinator.begin("TestCheckout/card/mastercard")
	assert.False(t, coordinator.finish(first))
	assert.False(t, coordinator.finish(second))

	isolated := coordinator.begin("TestCheckout/card/amex")
	assert.True(t, coordinator.finish(isolated))
}

func TestDirectQuarantinedRaceAttemptOwnersReturnsOnlyNearestFamilies(t *testing.T) {
	result := func(name string) processRetrySubtreeResult {
		return processRetrySubtreeResult{TestName: name, AttemptToFixOwn: true}
	}
	owners := directQuarantinedRaceAttemptOwners([]processRetrySubtreeResult{
		result("TestCheckout/card"),
		result("TestCheckout/card/owner"),
		result("TestCheckout/card/owner/clear/deep"),
		result("TestCheckout/card/sibling"),
	}, "TestCheckout/card")
	require.Len(t, owners, 2)
	assert.Equal(t, "TestCheckout/card/owner", owners[0].TestName)
	assert.Equal(t, "TestCheckout/card/sibling", owners[1].TestName)
}

func TestQuarantinedRaceContinuationConfigPreservesDeeperAttemptOwner(t *testing.T) {
	const module, suite = "module", "suite"
	cfg := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout",
		Root: processRetrySubtreeDirective{
			TestName: "TestCheckout", ModuleName: module, SuiteName: suite, Quarantined: true, AttemptToFix: true,
		},
		OwnsAttemptToFix: true,
		Directives: []processRetrySubtreeDirective{
			{TestName: "TestCheckout/clear", ModuleName: module, SuiteName: suite, Quarantined: true},
			{TestName: "TestCheckout/clear/owner", ModuleName: module, SuiteName: suite, Quarantined: true, AttemptToFix: true},
			{TestName: "TestCheckout/clear/owner/clear", ModuleName: module, SuiteName: suite, Quarantined: true},
			{TestName: "TestCheckout/clear/owner/clear/deep", ModuleName: module, SuiteName: suite, Quarantined: true, AttemptToFix: true},
		},
	}
	continuation, err := cfg.forSelectedRoot(module, suite, "TestCheckout/clear/owner")
	require.NoError(t, err)
	_, owner := continuation.resolveDirective(module, suite, "TestCheckout/clear/owner/clear/deep")
	assert.Equal(t, "TestCheckout/clear/owner/clear/deep", owner)
}

func TestQuarantinedRaceDirectiveResolutionUsesNearestAttemptOwner(t *testing.T) {
	const module, suite = "module", "suite"
	cfg := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout/card",
		Root: processRetrySubtreeDirective{
			TestName:    "TestCheckout/card",
			ModuleName:  module,
			SuiteName:   suite,
			Quarantined: true,
		},
		Directives: []processRetrySubtreeDirective{
			{TestName: "TestCheckout/card/visa", ModuleName: module, SuiteName: suite, Quarantined: true, AttemptToFix: true},
			{TestName: "TestCheckout/card/visa/credit", ModuleName: module, SuiteName: suite, Quarantined: true},
		},
	}

	directive, owner := cfg.resolveDirective(module, suite, "TestCheckout/card/visa")
	require.True(t, directive.AttemptToFix)
	assert.Equal(t, "TestCheckout/card/visa", owner)

	directive, owner = cfg.resolveDirective(module, suite, "TestCheckout/card/visa/debit")
	require.True(t, directive.AttemptToFix)
	assert.Equal(t, "TestCheckout/card/visa", owner)

	directive, owner = cfg.resolveDirective(module, suite, "TestCheckout/card/visa/credit")
	assert.False(t, directive.AttemptToFix)
	assert.Empty(t, owner)
}

func TestQuarantinedRaceAttemptToFixSettingIsTotalExecutionCount(t *testing.T) {
	assert.Equal(t, 1, processRetryAttemptToFixExecutionCount(0))
	assert.Equal(t, 1, processRetryAttemptToFixExecutionCount(1))
	assert.Equal(t, 3, processRetryAttemptToFixExecutionCount(3))
}

func TestQuarantinedRaceAggregateCoverageStartsAtSelectedRoot(t *testing.T) {
	starts, finishes := 0, 0
	state := newQuarantinedRaceChildState(&processRetrySubtreeConfig{SelectedRoot: "TestCheckout/card"})
	state.beginAggregate = func() { starts++ }
	state.finishAggregate = func() { finishes++ }

	state.beginAggregateCoverage("TestCheckout")
	state.beginAggregateCoverage("TestCheckout/card/visa")
	state.finishAggregateCoverage("TestCheckout")
	state.finishAggregateCoverage("TestCheckout/card/visa")
	assert.Zero(t, starts)
	assert.Zero(t, finishes)

	state.beginAggregateCoverage("TestCheckout/card")
	state.beginAggregateCoverage("TestCheckout/card")
	state.finishAggregateCoverage("TestCheckout/card")
	state.finishAggregateCoverage("TestCheckout/card")
	assert.Equal(t, 1, starts)
	assert.Equal(t, 1, finishes)
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

func TestQuarantinedRaceRootReplayKeepsNonRaceProcessOutput(t *testing.T) {
	cfg := &processRetrySubtreeConfig{
		SelectedRoot: "TestCheckout/card",
		Root:         processRetrySubtreeDirective{TestName: "TestCheckout/card", Quarantined: true},
	}
	invocation := quarantinedRaceInvocation{
		cfg: cfg,
		attempt: processRetryAttemptResult{
			OutputTail: "direct stdout\ndirect stderr",
			Result: processRetryResult{
				TestName: cfg.SelectedRoot, Status: processRetryStatusFail, Failed: true,
			},
		},
	}

	assert.Equal(t, invocation.attempt.OutputTail, processRetrySubtreeRootFromInvocation(invocation).OutputTail)
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

func TestQuarantinedRaceAggregatePayloadFitsWireLimit(t *testing.T) {
	for _, tt := range []struct {
		name     string
		count    int
		populate func(*processRetrySubtreeResult)
		check    func(*testing.T, processRetrySubtreeResult)
	}{
		{
			name:  "output",
			count: 2 * 1024,
			populate: func(result *processRetrySubtreeResult) {
				result.OutputTail = strings.Repeat("x", processRetrySubtreeOutputMaxBytes)
			},
			check: func(t *testing.T, result processRetrySubtreeResult) {
				assert.True(t, result.OutputTruncated)
				assert.Less(t, len(result.OutputTail), processRetrySubtreeOutputMaxBytes)
			},
		},
		{
			name:  "error stack",
			count: 512,
			populate: func(result *processRetrySubtreeResult) {
				result.ErrorStack = strings.Repeat("x", processRetryErrorStackMaxBytes)
			},
			check: func(t *testing.T, result processRetrySubtreeResult) {
				assert.Contains(t, result.ErrorStack, processRetryMetadataTruncationMarker)
				assert.Less(t, len(result.ErrorStack), processRetryErrorStackMaxBytes)
			},
		},
		{
			name:  "coverage",
			count: 1,
			populate: func(result *processRetrySubtreeResult) {
				result.Coverage = []coverage.ProcessTestCoverageFile{{
					Name: "file.go", Bitmap: bytes.Repeat([]byte{'x'}, processRetrySubtreeWireMaxBytes),
				}}
			},
			check: func(t *testing.T, result processRetrySubtreeResult) {
				assert.Empty(t, result.Coverage)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := "TestAggregate/root"
			cfg := processRetryChildConfig{
				ResultPath: filepath.Join(t.TempDir(), "result.json"),
				TestName:   root, Attempt: 1, RetryReason: processRetrySubtreeReason,
				Subtree: &processRetrySubtreeConfig{
					Version: processRetrySubtreeVersion, SelectedRoot: root,
					Root: processRetrySubtreeDirective{TestName: root, Quarantined: true},
				},
			}
			result := processRetryResult{
				Version: 1, TestName: root, ModuleName: "module", SuiteName: "suite", Attempt: 1, RetryReason: processRetrySubtreeReason,
				Status: processRetryStatusFail, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1, Failed: true,
				Subtests: make([]processRetrySubtreeResult, tt.count),
			}
			for idx := range result.Subtests {
				result.Subtests[idx] = processRetrySubtreeResult{
					TestName: fmt.Sprintf("%s/child-%04d", root, idx), ModuleName: "module", SuiteName: "suite",
					Status: processRetryStatusFail, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1,
					Failed: true, Quarantined: true, ErrorType: "error",
				}
				tt.populate(&result.Subtests[idx])
			}

			require.NoError(t, writeProcessRetryResultAtomically(cfg.ResultPath, result))
			payload, err := os.ReadFile(cfg.ResultPath)
			require.NoError(t, err)
			require.LessOrEqual(t, len(payload), processRetrySubtreeWireMaxBytes)
			got, _, err := readProcessRetryResult(cfg.ResultPath, cfg)
			require.NoError(t, err)
			require.Len(t, got.Subtests, tt.count)
			assert.Equal(t, root+"/child-0000", got.Subtests[0].TestName)
			assert.Equal(t, processRetryStatusFail, got.Subtests[0].Status)
			assert.True(t, got.Subtests[0].Failed)
			tt.check(t, got.Subtests[0])
			tt.check(t, got.Subtests[len(got.Subtests)-1])
		})
	}
}

func TestQuarantinedRaceSmallPayloadSerializationIsUnchanged(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.json")
	result := processRetryResult{
		Version: 1, TestName: "TestSmall/root", Attempt: 1, RetryReason: processRetrySubtreeReason,
		Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1,
		Subtests: []processRetrySubtreeResult{{
			TestName: "TestSmall/root/child", Status: processRetryStatusPass,
			StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1,
		}},
	}
	want, err := json.Marshal(result)
	require.NoError(t, err)

	require.NoError(t, writeProcessRetryResultAtomically(resultPath, result))
	got, err := os.ReadFile(resultPath)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestQuarantinedRaceUnrepresentableCoreWritesExplicitResult(t *testing.T) {
	root := "TestOversized/root"
	cfg := processRetryChildConfig{
		ResultPath: filepath.Join(t.TempDir(), "result.json"),
		TestName:   root, Attempt: 1, RetryReason: processRetrySubtreeReason,
		Subtree: &processRetrySubtreeConfig{
			Version: processRetrySubtreeVersion, SelectedRoot: root,
			Root: processRetrySubtreeDirective{TestName: root, Quarantined: true},
		},
	}
	result := processRetryResult{
		Version: 1, TestName: root, Attempt: 1, RetryReason: processRetrySubtreeReason,
		Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1,
		Subtests: []processRetrySubtreeResult{{
			TestName: root + "/" + strings.Repeat("x", processRetrySubtreeWireMaxBytes),
			Status:   processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1,
		}},
	}

	require.NoError(t, writeProcessRetryResultAtomically(cfg.ResultPath, result))
	got, timingOK, err := readProcessRetryResult(cfg.ResultPath, cfg)
	require.NoError(t, err)
	assert.False(t, timingOK)
	assert.Equal(t, processRetryStatusNotRun, got.Status)
	assert.Equal(t, processRetryResultErrorSubtreeTooLarge, got.ResultError)
	ordinary := cfg
	ordinary.Subtree = nil
	require.ErrorIs(t, validateProcessRetryResult(got, ordinary), errProcessRetryResultInvalid)
}

func TestQuarantinedRaceSubtreeConfigRejectsUntrustedDirectives(t *testing.T) {
	const module, suite = "module", "suite"
	valid := &processRetrySubtreeConfig{
		Version:      processRetrySubtreeVersion,
		SelectedRoot: "TestCheckout/card",
		Root: processRetrySubtreeDirective{
			TestName: "TestCheckout/card", ModuleName: module, SuiteName: suite,
			Quarantined: true,
		},
		Directives: []processRetrySubtreeDirective{{TestName: "TestCheckout/card/visa", ModuleName: module, SuiteName: suite, Quarantined: true}},
	}
	require.NoError(t, validateProcessRetrySubtreeConfig(valid, valid.SelectedRoot))

	outside := *valid
	outside.Directives = []processRetrySubtreeDirective{{TestName: "TestCheckout/paypal", ModuleName: module, SuiteName: suite, Quarantined: true}}
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
		ModuleName:        "module",
		SuiteName:         "suite",
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
			ModuleName:     "module",
			SuiteName:      "suite",
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

	missingIdentity := valid
	missingIdentity.ModuleName = ""
	require.Error(t, validateProcessRetryResult(missingIdentity, expected))

	missingSubtestIdentity := valid
	missingSubtestIdentity.Subtests = append([]processRetrySubtreeResult(nil), valid.Subtests...)
	missingSubtestIdentity.Subtests[0].SuiteName = ""
	require.Error(t, validateProcessRetryResult(missingSubtestIdentity, expected))

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
		Root: processRetrySubtreeDirective{
			TestName: "TestCheckout/card", ModuleName: "module", SuiteName: "suite", Quarantined: true,
		},
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

	attemptToFix := cfg
	attemptToFix.RetryReason = constants.AttemptToFixRetryReason
	attemptToFix.Subtree.AncestorAttemptToFix = true
	attemptToFix.Subtree.OwnsAttemptToFix = false
	require.NoError(t, writeProcessRetryControlConfig(path, attemptToFix))
	_, err = readProcessRetryControlConfig(path, processRetryChildConfig{
		TestName: attemptToFix.TestName, Attempt: attemptToFix.Attempt, RetryReason: attemptToFix.RetryReason,
		MRunEpoch: attemptToFix.MRunEpoch, InvocationOrdinal: attemptToFix.InvocationOrdinal, Subtree: attemptToFix.Subtree,
	})
	require.NoError(t, err)

	invalid := attemptToFix
	invalid.RetryReason = constants.AutoTestRetriesRetryReason
	require.Error(t, writeProcessRetryControlConfig(path, invalid))
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

func quarantinedRaceForeignSuiteCallback(*testing.T) {
	panic("disabled foreign-suite callback ran")
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
