// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
)

func exerciseAdditionalFeaturePathSelection(t *testing.T) {
	tests := []struct {
		name                  string
		meta                  additionalFeatureMetadata
		impactedTestsEnabled  bool
		flakyRetryCount       int64
		remainingFlakyRetries int64
		attemptToFixRetries   int
		efdRetryPossible      bool
		needsMetadataOnly     bool
		wantPath              additionalFeaturePath
		wantReasons           []string
	}{
		{
			name:     "test management enabled without directive does not wrap",
			meta:     additionalFeatureMetadata{isTestManagementEnabled: true},
			wantPath: additionalFeaturePathNone,
		},
		{
			name: "exact all false test management directive does not wrap",
			meta: additionalFeatureMetadata{
				isTestManagementEnabled: true,
				hasExplicitQuarantined:  true,
				hasExplicitDisabled:     true,
				hasExplicitAttemptToFix: true,
			},
			wantPath: additionalFeaturePathNone,
		},
		{
			name:        "disabled without attempt to fix uses disabled fast path",
			meta:        additionalFeatureMetadata{isTestManagementEnabled: true, isDisabled: true},
			wantPath:    additionalFeaturePathDisabledFast,
			wantReasons: []string{"test_management_disabled"},
		},
		{
			name:        "quarantined test still uses retry wrapper",
			meta:        additionalFeatureMetadata{isTestManagementEnabled: true, isQuarantined: true},
			wantPath:    additionalFeaturePathRetryWrapper,
			wantReasons: []string{"test_management_quarantined"},
		},
		{
			name:                "attempt to fix owns retry wrapper",
			meta:                additionalFeatureMetadata{isTestManagementEnabled: true, isAttemptToFix: true, shouldOrchestrateAttemptToFix: true},
			attemptToFixRetries: 3,
			wantPath:            additionalFeaturePathRetryWrapper,
			wantReasons:         []string{"attempt_to_fix"},
		},
		{
			name: "attempt to fix owns retry wrapper over EFD and flaky retry",
			meta: additionalFeatureMetadata{
				isAttemptToFix:                true,
				shouldOrchestrateAttemptToFix: true,
				isEarlyFlakeDetectionEnabled:  true,
				isFlakyTestRetriesEnabled:     true,
				isNew:                         true,
			},
			flakyRetryCount:       2,
			remainingFlakyRetries: 1,
			attemptToFixRetries:   3,
			wantPath:              additionalFeaturePathRetryWrapper,
			wantReasons:           []string{"attempt_to_fix"},
		},
		{
			name:        "attempt to fix with zero retries keeps metadata without retry group",
			meta:        additionalFeatureMetadata{isTestManagementEnabled: true, isAttemptToFix: true, shouldOrchestrateAttemptToFix: true},
			wantPath:    additionalFeaturePathMetadataOnly,
			wantReasons: []string{"attempt_to_fix_zero_retries"},
		},
		{
			name:        "masked attempt to fix with zero retries keeps isolated wrapper",
			meta:        additionalFeatureMetadata{isTestManagementEnabled: true, isQuarantined: true, isAttemptToFix: true, shouldOrchestrateAttemptToFix: true},
			wantPath:    additionalFeaturePathRetryWrapper,
			wantReasons: []string{"attempt_to_fix"},
		},
		{
			name:        "disabled attempt to fix still needs retry wrapper metadata",
			meta:        additionalFeatureMetadata{isTestManagementEnabled: true, isDisabled: true, isAttemptToFix: true},
			wantPath:    additionalFeaturePathRetryWrapper,
			wantReasons: []string{"test_management_disabled", "attempt_to_fix"},
		},
		{
			name:              "metadata only preserves inherited subtest override without retry wrapper",
			meta:              additionalFeatureMetadata{isTestManagementEnabled: true, hasExplicitAttemptToFix: true},
			needsMetadataOnly: true,
			wantPath:          additionalFeaturePathMetadataOnly,
			wantReasons:       []string{"inherited_subtest_state"},
		},
		{
			name:             "EFD new test uses retry wrapper",
			meta:             additionalFeatureMetadata{isEarlyFlakeDetectionEnabled: true, isNew: true},
			efdRetryPossible: true,
			wantPath:         additionalFeaturePathRetryWrapper,
			wantReasons:      []string{"efd_new_test"},
		},
		{
			name:        "EFD new test with zero retries keeps metadata without retry group",
			meta:        additionalFeatureMetadata{isEarlyFlakeDetectionEnabled: true, isNew: true},
			wantPath:    additionalFeaturePathMetadataOnly,
			wantReasons: []string{"efd_zero_retries"},
		},
		{
			name:     "EFD known test without impacted tests does not wrap",
			meta:     additionalFeatureMetadata{isEarlyFlakeDetectionEnabled: true},
			wantPath: additionalFeaturePathNone,
		},
		{
			name:                 "impacted tests keep conservative EFD wrapper",
			meta:                 additionalFeatureMetadata{isEarlyFlakeDetectionEnabled: true},
			impactedTestsEnabled: true,
			efdRetryPossible:     true,
			wantPath:             additionalFeaturePathRetryWrapper,
			wantReasons:          []string{"efd_modified_candidate"},
		},
		{
			name:                  "flaky retry budget selects retry wrapper",
			meta:                  additionalFeatureMetadata{isFlakyTestRetriesEnabled: true},
			flakyRetryCount:       2,
			remainingFlakyRetries: 1,
			wantPath:              additionalFeaturePathRetryWrapper,
			wantReasons:           []string{"flaky_retry"},
		},
		{
			name:                  "flaky retry without remaining budget does not wrap",
			meta:                  additionalFeatureMetadata{isFlakyTestRetriesEnabled: true},
			flakyRetryCount:       2,
			remainingFlakyRetries: 0,
			wantPath:              additionalFeaturePathNone,
		},
		{
			name:                  "flaky retry without per test retries does not wrap",
			meta:                  additionalFeatureMetadata{isFlakyTestRetriesEnabled: true},
			flakyRetryCount:       0,
			remainingFlakyRetries: 10,
			wantPath:              additionalFeaturePathNone,
		},
	}

	for _, tt := range tests {
		got := selectAdditionalFeaturePath(
			&tt.meta,
			tt.impactedTestsEnabled,
			tt.flakyRetryCount,
			tt.remainingFlakyRetries,
			tt.attemptToFixRetries,
			tt.efdRetryPossible,
			tt.needsMetadataOnly,
		)
		if got.path != tt.wantPath {
			t.Fatalf("%s: expected path %s, got %s", tt.name, tt.wantPath, got.path)
		}
		if !reflect.DeepEqual(got.reasons, tt.wantReasons) {
			t.Fatalf("%s: expected reasons %v, got %v", tt.name, tt.wantReasons, got.reasons)
		}
	}
}

func exerciseParallelEFDSelection(t *testing.T) {
	efdMeta := &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true}
	tests := []struct {
		name              string
		options           runTestWithRetryOptions
		meta              *testExecutionMetadata
		remainingAttempts int64
		maxConcurrency    int64
		want              bool
	}{
		{
			name:              "disabled when internal flag is false",
			options:           runTestWithRetryOptions{parallelEFDAllowed: false},
			meta:              efdMeta,
			remainingAttempts: 2,
			maxConcurrency:    4,
		},
		{
			name:              "disabled when execution is not EFD owned",
			options:           runTestWithRetryOptions{parallelEFDAllowed: true},
			meta:              &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true},
			remainingAttempts: 2,
			maxConcurrency:    4,
		},
		{
			name:              "disabled for attempt to fix owner",
			options:           runTestWithRetryOptions{parallelEFDAllowed: true},
			meta:              &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true, isAttemptToFix: true, shouldOrchestrateAttemptToFix: true},
			remainingAttempts: 2,
			maxConcurrency:    4,
		},
		{
			name:              "disabled when only one attempt remains",
			options:           runTestWithRetryOptions{parallelEFDAllowed: true},
			meta:              efdMeta,
			remainingAttempts: 1,
			maxConcurrency:    4,
		},
		{
			name:              "disabled when effective max concurrency is sequential",
			options:           runTestWithRetryOptions{parallelEFDAllowed: true},
			meta:              efdMeta,
			remainingAttempts: 2,
			maxConcurrency:    1,
		},
		{
			name:              "enabled for EFD owner with concurrent retry attempts",
			options:           runTestWithRetryOptions{parallelEFDAllowed: true},
			meta:              efdMeta,
			remainingAttempts: 2,
			maxConcurrency:    4,
			want:              true,
		},
	}

	for _, tt := range tests {
		if got := shouldUseParallelEFD(&tt.options, tt.meta, tt.remainingAttempts, tt.maxConcurrency); got != tt.want {
			t.Fatalf("%s: expected %t, got %t", tt.name, tt.want, got)
		}
	}
}

func exerciseMetadataOnlyPropagationSuppression(t *testing.T) {
	parentMeta := &testExecutionMetadata{
		isANewTest:                   true,
		isAModifiedTest:              true,
		isARetry:                     true,
		isEarlyFlakeDetectionEnabled: true,
		isFlakyTestRetriesEnabled:    true,
		isQuarantined:                true,
		isDisabled:                   true,
		isAttemptToFix:               true,
		isEfdInParallel:              true,
		hasAdditionalFeatureWrapper:  true,
	}
	childMeta := &testExecutionMetadata{
		hasExplicitAttemptToFix:     true,
		suppressParentRetryMetadata: true,
	}

	propagateTestExecutionMetadataFlags(childMeta, parentMeta)

	if childMeta.isARetry {
		t.Fatal("expected retry tag ownership not to propagate")
	}
	if childMeta.isEfdInParallel {
		t.Fatal("expected parallel EFD execution state not to propagate")
	}
	if childMeta.hasAdditionalFeatureWrapper {
		t.Fatal("expected wrapper ownership not to propagate")
	}
	if childMeta.isAttemptToFix {
		t.Fatal("expected explicit attempt-to-fix false override to be preserved")
	}
	if !childMeta.isANewTest || !childMeta.isAModifiedTest || !childMeta.isEarlyFlakeDetectionEnabled || !childMeta.isFlakyTestRetriesEnabled {
		t.Fatal("expected product metadata to continue propagating")
	}
	if !childMeta.isQuarantined || !childMeta.isDisabled {
		t.Fatal("expected current disabled and quarantine inheritance semantics to be preserved")
	}
}

func exerciseSlowEFDAbortTagging(t *testing.T) {
	tests := []struct {
		name     string
		meta     testExecutionMetadata
		duration time.Duration
		wantTag  bool
	}{
		{
			name:     "new EFD test at threshold",
			meta:     testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true},
			duration: 5 * time.Minute,
			wantTag:  true,
		},
		{
			name:     "modified EFD test above threshold",
			meta:     testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isAModifiedTest: true},
			duration: 6 * time.Minute,
			wantTag:  true,
		},
		{
			name:     "EFD fallback to flaky retries remains an aborted EFD execution",
			meta:     testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true, efdFellBackToFlakyRetries: true},
			duration: 5 * time.Minute,
			wantTag:  true,
		},
		{
			name:     "new test with EFD disabled",
			meta:     testExecutionMetadata{isANewTest: true},
			duration: 5 * time.Minute,
		},
		{
			name:     "attempt to fix takes precedence over EFD",
			meta:     testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true, isAttemptToFix: true},
			duration: 5 * time.Minute,
		},
		{
			name:     "EFD test below threshold",
			meta:     testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true},
			duration: 5*time.Minute - time.Nanosecond,
		},
	}

	for i := range tests {
		tt := &tests[i]
		test := &processRetryRecordingTest{}
		finalizeInstrumentedTestExecution(t, &tt.meta, test, nil, nil, tt.duration, nil, nil, "", false)
		_, gotTag := test.GetTag(constants.TestEarlyFlakeDetectionRetryAborted)
		if gotTag != tt.wantTag {
			t.Fatalf("%s: expected slow EFD abort tag=%t, got %t", tt.name, tt.wantTag, gotTag)
		}
	}
}
