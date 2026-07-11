// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	internalLog "github.com/DataDog/dd-trace-go/v2/internal/log"
	coretelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestRetryExecutionModeFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want retryExecutionMode
	}{
		{name: "unset", want: retryExecutionModeInProcess},
		{name: "in_process", env: "in_process", want: retryExecutionModeInProcess},
		{name: "process", env: "process", want: retryExecutionModeProcess},
		{name: "mixed case with whitespace", env: " PROCESS ", want: retryExecutionModeProcess},
		{name: "invalid fallback", env: "fork", want: retryExecutionModeInProcess},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, tt.env)
			}
			require.Equal(t, tt.want, retryExecutionModeFromEnv())
		})
	}
}

func TestProcessRetryUnitRunFilterUsesExactTopLevelNames(t *testing.T) {
	filter := regexp.MustCompile(buildProcessRetryUnitRunFilter([]testing.InternalTest{
		{Name: "TestProcessRetryAllowed"},
		{Name: "TestRecordProcessRetryPanicPreservesFirstPanic"},
		{Name: "TestUnrelated"},
	}))
	require.True(t, filter.MatchString("TestProcessRetryAllowed"))
	require.True(t, filter.MatchString("TestProcessRetryAllowed/subtest"))
	require.True(t, filter.MatchString("TestRecordProcessRetryPanicPreservesFirstPanic"))
	require.False(t, filter.MatchString("TestProcessRetryAllowedSuffix"))
	require.False(t, filter.MatchString("TestUnrelated"))
}

func TestProcessRetryMaxConcurrencyFromEnv(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		defaultVal int
		want       int
	}{
		{name: "unset uses default", defaultVal: 3, want: 3},
		{name: "default clamped", defaultVal: 0, want: 1},
		{name: "valid env", env: "4", defaultVal: 1, want: 4},
		{name: "invalid env", env: "invalid", defaultVal: 2, want: 2},
		{name: "zero env", env: "0", defaultVal: 2, want: 2},
		{name: "negative env", env: "-1", defaultVal: 2, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, tt.env)
			}
			require.Equal(t, tt.want, processRetryMaxConcurrencyFromEnv(tt.defaultVal))
		})
	}
}

func TestProcessRetryTimeoutFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
		ok   bool
	}{
		{name: "unset"},
		{name: "valid", env: "250ms", want: 250 * time.Millisecond, ok: true},
		{name: "invalid", env: "invalid"},
		{name: "zero", env: "0"},
		{name: "negative", env: "-1s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable, tt.env)
			}
			got, ok := processRetryTimeoutFromEnv()
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestProcessRetrySelectedTimeoutUsesDefaultUnlessShortened(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	require.Equal(t, processRetryDefaultTimeout, selectedProcessRetryTimeout(
		30*time.Minute, true, 0, false, time.Time{}, false, now,
	))
	require.Equal(t, 5*time.Minute, selectedProcessRetryTimeout(
		5*time.Minute, true, 0, false, time.Time{}, false, now,
	))
	require.Equal(t, 20*time.Minute, selectedProcessRetryTimeout(
		30*time.Minute, true, 20*time.Minute, true, time.Time{}, false, now,
	))
}

func TestProcessRetryChildConfigFromEnv(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityInternalRetryProcessChild, "true")
		t.Setenv(constants.CIVisibilityInternalRetryProcessResultPath, "/tmp/result.json")
		t.Setenv(constants.CIVisibilityInternalRetryProcessTestName, "TestProcess")
		t.Setenv(constants.CIVisibilityInternalRetryProcessAttempt, "1")
		t.Setenv(constants.CIVisibilityInternalRetryProcessReason, constants.AutoTestRetriesRetryReason)

		require.True(t, isProcessRetryChild())
		cfg, err := processRetryChildConfigFromEnv()
		require.NoError(t, err)
		require.Equal(t, "/tmp/result.json", cfg.ResultPath)
		require.Equal(t, "TestProcess", cfg.TestName)
		require.Equal(t, 1, cfg.Attempt)
		require.Equal(t, constants.AutoTestRetriesRetryReason, cfg.RetryReason)
	})

	t.Run("invalid bool disables child mode", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityInternalRetryProcessChild, "not-bool")
		require.False(t, isProcessRetryChild())
	})

	t.Run("missing attempt", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityInternalRetryProcessResultPath, "/tmp/result.json")
		t.Setenv(constants.CIVisibilityInternalRetryProcessTestName, "TestProcess")
		t.Setenv(constants.CIVisibilityInternalRetryProcessReason, constants.AutoTestRetriesRetryReason)

		_, err := processRetryChildConfigFromEnv()
		require.ErrorIs(t, err, errProcessRetryMissingAttempt)
		require.Equal(t, "missing_attempt", processRetryChildConfigErrorReason(err))
	})

	t.Run("attempt zero", func(t *testing.T) {
		t.Setenv(constants.CIVisibilityInternalRetryProcessResultPath, "/tmp/result.json")
		t.Setenv(constants.CIVisibilityInternalRetryProcessTestName, "TestProcess")
		t.Setenv(constants.CIVisibilityInternalRetryProcessAttempt, "0")
		t.Setenv(constants.CIVisibilityInternalRetryProcessReason, constants.AutoTestRetriesRetryReason)

		_, err := processRetryChildConfigFromEnv()
		require.ErrorIs(t, err, errProcessRetryInvalidAttempt)
		require.Equal(t, "invalid_attempt", processRetryChildConfigErrorReason(err))
	})
}

func TestProcessRetryEligible(t *testing.T) {
	identity := newTestIdentity("module", "suite", "TestProcess")
	baseExecMeta := func() *testExecutionMetadata {
		return &testExecutionMetadata{
			identity:                  identity,
			isFlakyTestRetriesEnabled: true,
		}
	}
	baseOptions := func() *runTestWithRetryOptions {
		return &runTestWithRetryOptions{
			testInfo:             &commonInfo{moduleName: "module", suiteName: "suite", testName: "TestProcess", identity: identity},
			processRetryAllowed:  true,
			processRetryIdentity: identity,
			coverageActive:       func() bool { return false },
			fuzzActive:           func() bool { return false },
			preProcessRetryMetaAdjust: func(*testExecutionMetadata, int) {
			},
		}
	}

	tests := []struct {
		name            string
		mode            string
		hooks           processRetrySupportHooks
		childMode       bool
		disableLaunches bool
		nilMeta         bool
		nilOptions      bool
		editMeta        func(*testExecutionMetadata)
		editOpts        func(*runTestWithRetryOptions)
		wantOK          bool
		wantCause       string
	}{
		{
			name:   "eligible top-level FTR",
			mode:   "process",
			wantOK: true,
		},
		{
			name:      "unset mode falls back",
			wantCause: "mode_in_process",
		},
		{
			name:      "child mode is ineligible",
			mode:      "process",
			childMode: true,
			wantCause: "child_mode",
		},
		{
			name:       "missing options",
			mode:       "process",
			nilOptions: true,
			wantCause:  "missing_options",
		},
		{
			name: "process not allowed for wrapper",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.processRetryAllowed = false
			},
			wantCause: "process_retry_not_allowed",
		},
		{
			name:            "process launches disabled after unreaped child",
			mode:            "process",
			disableLaunches: true,
			wantCause:       "process_launch_disabled",
		},
		{
			name: "missing process identity",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.processRetryIdentity = nil
			},
			wantCause: "missing_identity",
		},
		{
			name: "missing test info",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.testInfo = nil
			},
			wantCause: "missing_test_info",
		},
		{
			name: "incomplete test info",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.testInfo.testName = ""
			},
			wantCause: "incomplete_test_info",
		},
		{
			name: "subtest is ineligible",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.processRetryIdentity = newTestIdentity("module", "suite", "TestProcess/Sub")
			},
			wantCause: "subtest",
		},
		{
			name: "actual EFD execution is ineligible",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.isEarlyFlakeDetectionEnabled = true
				meta.isANewTest = true
			},
			wantCause: "efd",
		},
		{
			name: "coverage is ineligible",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.coverageActive = func() bool { return true }
			},
			wantCause: "coverage_active",
		},
		{
			name: "fuzz is ineligible",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.fuzzActive = func() bool { return true }
			},
			wantCause: "fuzz_active",
		},
		{
			name: "testing layout unsupported",
			mode: "process",
			hooks: processRetrySupportHooks{
				childCleanupSupported: func() bool { return false },
			},
			wantCause: "testing_t_layout_unsupported",
		},
		{
			name: "testing M workload layout unsupported",
			mode: "process",
			hooks: processRetrySupportHooks{
				testingMWorkloadsSupported: func() bool { return false },
			},
			wantCause: "testing_m_layout_unsupported",
		},
		{
			name:      "missing execution metadata",
			mode:      "process",
			nilMeta:   true,
			wantCause: "missing_execution_metadata",
		},
		{
			name: "missing execution identity",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.identity = nil
			},
			wantCause: "missing_execution_identity",
		},
		{
			name: "execution identity mismatch",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.identity = newTestIdentity("module", "suite", "TestOther")
			},
			wantCause: "identity_mismatch",
		},
		{
			name: "execution subtest is ineligible",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.identity = &testIdentity{
					ModuleName: "module",
					SuiteName:  "suite",
					BaseName:   "TestProcess",
					FullName:   "TestProcess",
					Segments:   []string{"TestProcess", "Sub"},
				}
			},
			wantCause: "subtest",
		},
		{
			name: "missing process metadata callback",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.preProcessRetryMetaAdjust = nil
			},
			wantCause: "missing_process_metadata_callback",
		},
		{
			name: "flaky retries disabled",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.isFlakyTestRetriesEnabled = false
			},
			wantCause: "flaky_retry_disabled",
		},
		{
			name: "attempt to fix is ineligible",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.isAttemptToFix = true
			},
			wantCause: "attempt_to_fix",
		},
		{
			name: "quarantined test is ineligible",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.isQuarantined = true
			},
			wantCause: "quarantined",
		},
		{
			name: "disabled test is ineligible",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.isDisabled = true
			},
			wantCause: "disabled",
		},
		{
			name: "missing coverage guard",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.coverageActive = nil
			},
			wantCause: "missing_coverage_guard",
		},
		{
			name: "missing fuzz guard",
			mode: "process",
			editOpts: func(opts *runTestWithRetryOptions) {
				opts.fuzzActive = nil
			},
			wantCause: "missing_fuzz_guard",
		},
		{
			name: "parallel EFD is ineligible",
			mode: "process",
			editMeta: func(meta *testExecutionMetadata) {
				meta.isEfdInParallel = true
			},
			wantCause: "parallel_efd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
			defer restoreLaunchGate()
			if tt.mode != "" {
				t.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, tt.mode)
			}
			if tt.childMode {
				enableProcessRetryChildForTesting(t)
			}
			if tt.disableLaunches {
				disableProcessRetryLaunches()
			}
			hooks := tt.hooks
			if hooks.childCleanupSupported == nil {
				hooks.childCleanupSupported = func() bool { return true }
			}
			restore := setProcessRetrySupportHooksForTesting(t, hooks)
			defer restore()

			var execMeta *testExecutionMetadata
			if !tt.nilMeta {
				execMeta = baseExecMeta()
			}
			var options *runTestWithRetryOptions
			if !tt.nilOptions {
				options = baseOptions()
			}
			if tt.editMeta != nil && execMeta != nil {
				tt.editMeta(execMeta)
			}
			if tt.editOpts != nil && options != nil {
				tt.editOpts(options)
			}

			ok, reason := processRetryEligible(execMeta, options)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantCause, reason)
		})
	}
}

func TestProcessRetryCoverageActive(t *testing.T) {
	require.True(t, processRetryCoverageActive(true))

	t.Run("coverprofile flag", func(t *testing.T) {
		fs := useIsolatedProcessRetryFlagSet(t)
		fs.String("test.coverprofile", "", "")
		require.NoError(t, fs.Parse([]string{"-test.coverprofile=cover.out"}))
		require.True(t, processRetryCoverageActive(false))
	})

	t.Run("gocoverdir flag", func(t *testing.T) {
		fs := useIsolatedProcessRetryFlagSet(t)
		fs.String("test.gocoverdir", "", "")
		require.NoError(t, fs.Parse([]string{"-test.gocoverdir=gocover"}))
		require.True(t, processRetryCoverageActive(false))
	})

	t.Run("inactive flags", func(t *testing.T) {
		fs := useIsolatedProcessRetryFlagSet(t)
		fs.String("test.coverprofile", "", "")
		fs.String("test.gocoverdir", "", "")
		require.NoError(t, fs.Parse(nil))
		require.Equal(t, testing.CoverMode() != "", processRetryCoverageActive(false))
	})
}

func TestProcessRetryFuzzActive(t *testing.T) {
	tests := []struct {
		name     string
		register func(*flag.FlagSet)
		args     []string
		want     bool
	}{
		{
			name: "test fuzz",
			register: func(fs *flag.FlagSet) {
				fs.String("test.fuzz", "", "")
			},
			args: []string{"-test.fuzz=FuzzTarget"},
			want: true,
		},
		{
			name: "short fuzz",
			register: func(fs *flag.FlagSet) {
				fs.String("fuzz", "", "")
			},
			args: []string{"-fuzz=FuzzTarget"},
			want: true,
		},
		{
			name: "fuzz cache",
			register: func(fs *flag.FlagSet) {
				fs.String("test.fuzzcachedir", "", "")
			},
			args: []string{"-test.fuzzcachedir=cache"},
			want: true,
		},
		{
			name: "fuzz worker",
			register: func(fs *flag.FlagSet) {
				fs.Bool("test.fuzzworker", false, "")
			},
			args: []string{"-test.fuzzworker"},
			want: true,
		},
		{
			name: "fuzz time",
			register: func(fs *flag.FlagSet) {
				fs.Duration("test.fuzztime", 0, "")
			},
			args: []string{"-test.fuzztime=1s"},
			want: true,
		},
		{
			name: "default fuzz time does not count",
			register: func(fs *flag.FlagSet) {
				fs.Duration("test.fuzztime", time.Second, "")
				fs.Duration("test.fuzzminimizetime", time.Second, "")
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := useIsolatedProcessRetryFlagSet(t)
			tt.register(fs)
			require.NoError(t, fs.Parse(tt.args))
			require.Equal(t, tt.want, processRetryFuzzActive())
		})
	}
}

func TestProcessRetryChildBypassesOrchestrionInstrumentation(t *testing.T) {
	enableProcessRetryChildForTesting(t)

	called := false
	originalTest := func(*testing.T) { called = true }
	gotTest := instrumentTestingTFunc(originalTest)
	require.NotEqual(t, functionPointer(originalTest), functionPointer(gotTest))
	gotTest(t)
	require.True(t, called)

	originalBenchmark := func(*testing.B) {}
	gotName, gotBenchmark := instrumentTestingBFunc(nil, "BenchmarkProcessRetryChild", originalBenchmark)
	require.Equal(t, "BenchmarkProcessRetryChild", gotName)
	require.Equal(t, functionPointer(originalBenchmark), functionPointer(gotBenchmark))
}

func TestProcessRetryChildSubtestErrorForwardsWithoutOverwritingTopLevelSkip(t *testing.T) {
	enableProcessRetryChildForTesting(t)

	spy := &processRetrySpyTest{name: t.Name(), ctx: context.Background()}
	owner := createTestMetadata(t, nil)
	owner.test = spy
	defer deleteTestMetadata(t)

	wrapper := instrumentTestingTFunc(func(subtest *testing.T) {
		instrumentSetErrorInfo(subtest, "assertion", "subtest error sentinel", 0)
		instrumentCloseAndSkip(subtest, "subtest skip sentinel")
		require.Same(subtest, spy, getTestOptimizationTest(subtest))
	})
	require.True(t, t.Run("subtest", wrapper))

	errorInfo := owner.processRetryError.Load()
	require.NotNil(t, errorInfo)
	require.Equal(t, "assertion", errorInfo.Type)
	require.Equal(t, "subtest error sentinel", errorInfo.Message)
	require.NotEmpty(t, errorInfo.Stack)
	require.Nil(t, owner.processRetrySkipReason.Load())
	instrumentCloseAndSkip(t, "top-level skip sentinel")
	skipReason := owner.processRetrySkipReason.Load()
	require.NotNil(t, skipReason)
	require.Equal(t, "top-level skip sentinel", *skipReason)
	require.Zero(t, spy.setErrorCalls.Load())
	require.Zero(t, spy.closeCalls.Load())
}

func TestProcessRetryChildCapturesMetadataWithoutSpanOwnership(t *testing.T) {
	enableProcessRetryChildForTesting(t)

	spy := &processRetrySpyTest{name: t.Name(), ctx: context.WithValue(context.Background(), processRetrySpyContextKey{}, "metadata")}
	meta := createTestMetadata(t, nil)
	meta.test = spy
	defer deleteTestMetadata(t)

	instrumentSetErrorInfo(t, "panic", "message", 0)
	instrumentCloseAndSkip(t, "skip reason")
	instrumentSkipNow(t)
	instrumentTestifySuiteRun(t, struct{}{})

	require.Equal(t, int32(0), spy.setErrorCalls.Load())
	require.Equal(t, int32(0), spy.setTagCalls.Load())
	require.Equal(t, int32(0), spy.closeCalls.Load())
	errorInfo := meta.processRetryError.Load()
	require.NotNil(t, errorInfo)
	require.Equal(t, "panic", errorInfo.Type)
	require.Equal(t, "message", errorInfo.Message)
	require.NotEmpty(t, errorInfo.Stack)
	skipReason := meta.processRetrySkipReason.Load()
	require.NotNil(t, skipReason)
	require.Equal(t, "skip reason", *skipReason)
	require.Equal(t, context.Background(), getTestOptimizationContext(t))
	require.Same(t, spy, getTestOptimizationTest(t))
}

func TestProcessRetryChildBenchmarkContextBypassesMetadata(t *testing.T) {
	enableProcessRetryChildForTesting(t)

	bench := &testing.B{}
	spy := &processRetrySpyTest{name: "BenchmarkProcessRetryChild", ctx: context.WithValue(context.Background(), processRetrySpyContextKey{}, "metadata")}
	meta := createTestMetadata(bench, nil)
	meta.test = spy
	defer deleteTestMetadata(bench)

	require.Equal(t, context.Background(), (*B)(bench).Context())
}

func TestProcessRetryChildInvalidConfig(t *testing.T) {
	enableProcessRetryChildForTesting(t)
	resultPath := filepath.Join(t.TempDir(), "result.json")
	restore := setEnvForTesting(t,
		constants.CIVisibilityInternalRetryProcessResultPath, resultPath,
		constants.CIVisibilityInternalRetryProcessAttempt, "1",
		constants.CIVisibilityInternalRetryProcessReason, constants.AutoTestRetriesRetryReason,
	)
	defer restore()

	require.Equal(t, 1, runProcessRetryChild(nil))

	result := readProcessRetryResultForTesting(t, resultPath)
	require.Equal(t, 1, result.Version)
	require.Equal(t, processRetryStatusNotRun, result.Status)
	require.Equal(t, "missing_test_name", result.ResultError)
	require.Empty(t, result.TestName)
	require.Zero(t, result.Attempt)
	require.Empty(t, result.RetryReason)
}

func TestProcessRetryInvalidConfigResultPreservesParsedIdentity(t *testing.T) {
	cfg := processRetryChildConfig{
		ResultPath:  filepath.Join(t.TempDir(), "result.json"),
		TestName:    "TestParsedIdentity",
		Attempt:     2,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	writeInvalidProcessRetryChildConfigResult(cfg, "invalid_child_config")

	result, timingOK, err := readProcessRetryResult(cfg.ResultPath, cfg)
	require.NoError(t, err)
	require.False(t, timingOK)
	require.Equal(t, processRetryStatusNotRun, result.Status)
	require.Equal(t, cfg.TestName, result.TestName)
	require.Equal(t, cfg.Attempt, result.Attempt)
	require.Equal(t, cfg.RetryReason, result.RetryReason)
	require.Equal(t, "invalid_child_config", result.ResultError)
}

func TestProcessRetryResultErrorValidation(t *testing.T) {
	cfg := processRetryChildConfig{
		TestName:    "TestResultErrorValidation",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	base := processRetryNotRunResult(cfg, "invalid_child_config")
	require.NoError(t, validateProcessRetryResult(base, cfg))

	unknown := base
	unknown.ResultError = "unknown_reason"
	require.ErrorIs(t, validateProcessRetryResult(unknown, cfg), errProcessRetryResultInvalid)

	escapedOversized := base
	escapedOversized.ResultError = strings.Repeat("\n", processRetryResultErrorMaxBytes)
	require.ErrorIs(t, validateProcessRetryResult(escapedOversized, cfg), errProcessRetryResultInvalid)
}

func TestDisableProcessRetryChildExecution(t *testing.T) {
	m := &testing.M{}
	tests := getInternalTestArray(m)
	benchmarks := getInternalBenchmarkArray(m)
	fuzzTargets := getInternalFuzzTargetArray(m)
	examples := getInternalExampleArray(m)
	require.NotNil(t, tests)
	require.NotNil(t, benchmarks)
	require.NotNil(t, fuzzTargets)
	require.NotNil(t, examples)

	*tests = []testing.InternalTest{{Name: "TestProcessRetryChild", F: func(*testing.T) {}}}
	*benchmarks = []testing.InternalBenchmark{{Name: "BenchmarkProcessRetryChild", F: func(*testing.B) {}}}
	*fuzzTargets = []testing.InternalFuzzTarget{{Name: "FuzzProcessRetryChild", Fn: func(*testing.F) {}}}
	*examples = []testing.InternalExample{{Name: "ExampleProcessRetryChild", F: func() {}}}

	require.True(t, disableProcessRetryChildExecution(m))
	require.Empty(t, *tests)
	require.Empty(t, *benchmarks)
	require.Empty(t, *fuzzTargets)
	require.Empty(t, *examples)
}

func TestProcessRetryChildCleanupSupported(t *testing.T) {
	layout := getTestingInternalsLayout()
	require.NotNil(t, layout)
	require.False(t, layout.disabled)
	require.True(t, processRetryChildCleanupSupported())
}

func TestProcessRetrySupportHooksRestoreIsIdempotent(t *testing.T) {
	before := processRetrySupportHooksOverride.Load()
	restore := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return false },
		testingMWorkloadsSupported: func() bool { return false },
	})
	defer restore()
	require.NotEqual(t, before, processRetrySupportHooksOverride.Load())
	restore()
	restore()
	require.Equal(t, before, processRetrySupportHooksOverride.Load())
}

func TestProcessRetryTestingMReflectionDrift(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.json")
	cfg := processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    "TestSelected",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	ran := atomic.Bool{}
	tests := []testing.InternalTest{{Name: cfg.TestName, F: func(*testing.T) { ran.Store(true) }}}
	benchmarks := []testing.InternalBenchmark{{Name: "BenchmarkSelected", F: func(*testing.B) { ran.Store(true) }}}
	examples := []testing.InternalExample{{Name: "ExampleSelected", F: func() { ran.Store(true) }}}
	hardStopReason := ""

	configureProcessRetryChildWorkloads(
		cfg,
		newProcessRetryResultWriter(resultPath),
		func(int) {},
		&tests,
		&benchmarks,
		nil,
		&examples,
		func(reason string) { hardStopReason = reason },
	)

	require.Equal(t, "testing_m_reflection_drift", hardStopReason)
	require.Empty(t, tests)
	require.Empty(t, benchmarks)
	require.Empty(t, examples)
	require.False(t, ran.Load())
	result, _, err := readProcessRetryResult(resultPath, cfg)
	require.NoError(t, err)
	require.Equal(t, processRetryStatusNotRun, result.Status)
	require.Equal(t, "testing_m_reflection_drift", result.ResultError)
}

func TestProcessRetryTestingMReflectionFields(t *testing.T) {
	assertProcessRetryTestingMReflectionFields(t)
}

func assertProcessRetryTestingMReflectionFields(t *testing.T) {
	t.Helper()
	m := &testing.M{}
	require.NotNil(t, getInternalTestArray(m))
	require.NotNil(t, getInternalBenchmarkArray(m))
	require.NotNil(t, getInternalFuzzTargetArray(m))
	require.NotNil(t, getInternalExampleArray(m))
	require.True(t, processRetryTestingMWorkloadsSupportedDefault())
}

func TestProcessRetryChildWritesResult(t *testing.T) {
	m := &testing.M{}
	tests := getInternalTestArray(m)
	benchmarks := getInternalBenchmarkArray(m)
	fuzzTargets := getInternalFuzzTargetArray(m)
	examples := getInternalExampleArray(m)
	require.NotNil(t, tests)
	require.NotNil(t, benchmarks)
	require.NotNil(t, fuzzTargets)
	require.NotNil(t, examples)

	ran := false
	*tests = []testing.InternalTest{
		{Name: "TestSelected", F: func(*testing.T) { ran = true }},
		{Name: "TestOther", F: func(*testing.T) { t.Fatal("unselected test ran") }},
	}
	*benchmarks = []testing.InternalBenchmark{{Name: "BenchmarkOther", F: func(*testing.B) { t.Fatal("benchmark ran") }}}
	*fuzzTargets = []testing.InternalFuzzTarget{{Name: "FuzzOther", Fn: func(*testing.F) { t.Fatal("fuzz target ran") }}}
	*examples = []testing.InternalExample{{Name: "ExampleOther", F: func() { t.Fatal("example ran") }}}

	tempDir, cleanupTempDir := manualTempDirForTesting(t)
	defer cleanupTempDir()
	resultPath := filepath.Join(tempDir, "result.json")
	cfg := processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    "TestSelected",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	finalize := instrumentProcessRetryChild(m, cfg)
	wrappedPointer := functionPointer((*tests)[0].F)
	duplicateFinalize := instrumentProcessRetryChild(m, cfg)
	require.Equal(t, wrappedPointer, functionPointer((*tests)[0].F))
	require.Len(t, *tests, 1)
	require.Empty(t, *benchmarks)
	require.Empty(t, *fuzzTargets)
	require.Empty(t, *examples)

	(*tests)[0].F(t)
	finalize(0)
	duplicateFinalize(0)

	require.True(t, ran)
	result := readProcessRetryResultForTesting(t, resultPath)
	require.Equal(t, 1, result.Version)
	require.Equal(t, "TestSelected", result.TestName)
	require.Equal(t, 1, result.Attempt)
	require.Equal(t, constants.AutoTestRetriesRetryReason, result.RetryReason)
	require.Equal(t, processRetryStatusPass, result.Status)
	require.False(t, result.Failed)
	require.False(t, result.Skipped)
	require.Positive(t, result.StartUnixNano)
	require.GreaterOrEqual(t, result.FinishUnixNano, result.StartUnixNano)
}

func TestProcessRetryChildWritesNotRunWhenSelectedTestIsMissing(t *testing.T) {
	m := &testing.M{}
	tests := getInternalTestArray(m)
	benchmarks := getInternalBenchmarkArray(m)
	fuzzTargets := getInternalFuzzTargetArray(m)
	examples := getInternalExampleArray(m)
	require.NotNil(t, tests)
	require.NotNil(t, benchmarks)
	require.NotNil(t, fuzzTargets)
	require.NotNil(t, examples)

	*tests = []testing.InternalTest{{Name: "TestOther", F: func(*testing.T) { t.Fatal("unselected test ran") }}}
	*benchmarks = []testing.InternalBenchmark{{Name: "BenchmarkOther", F: func(*testing.B) { t.Fatal("benchmark ran") }}}
	*fuzzTargets = []testing.InternalFuzzTarget{{Name: "FuzzOther", Fn: func(*testing.F) { t.Fatal("fuzz target ran") }}}
	*examples = []testing.InternalExample{{Name: "ExampleOther", F: func() { t.Fatal("example ran") }}}

	resultPath := filepath.Join(t.TempDir(), "result.json")
	cfg := processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    "TestSelected",
		Attempt:     2,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	finalize := instrumentProcessRetryChild(m, cfg)
	finalize(0)

	require.Empty(t, *tests)
	require.Empty(t, *benchmarks)
	require.Empty(t, *fuzzTargets)
	require.Empty(t, *examples)
	result := readProcessRetryResultForTesting(t, resultPath)
	require.Equal(t, processRetryStatusNotRun, result.Status)
	require.Equal(t, "TestSelected", result.TestName)
	require.Equal(t, 2, result.Attempt)
}

func TestProcessRetryNoopTestContextAndSessionChain(t *testing.T) {
	cfg := processRetryChildConfig{
		TestName:    "TestSelected",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	ciTest := newProcessRetryNoopTest(cfg, time.Now())

	require.Equal(t, context.Background(), ciTest.Context())
	require.Equal(t, "TestSelected", ciTest.Name())
	require.Equal(t, uint64(0), ciTest.TestID())
	require.NotPanics(t, func() {
		ciTest.SetError()
		ciTest.SetTag("key", "value")
		ciTest.SetTestFunc(nil)
		ciTest.SetBenchmarkData("duration", map[string]any{"mean": 1})
		ciTest.Log("message", "")
		ciTest.Close(integrations.ResultStatusPass)
	})
	value, ok := ciTest.GetTag("key")
	require.False(t, ok)
	require.Nil(t, value)

	suite := ciTest.Suite()
	require.NotNil(t, suite)
	module := suite.Module()
	require.NotNil(t, module)
	session := module.Session()
	require.NotNil(t, session)
	require.Equal(t, context.Background(), session.Context())
	require.Equal(t, "go", module.Framework())
	require.Equal(t, "go", session.Framework())
	require.Equal(t, "child", suite.CreateTest("child").Name())
	require.Equal(t, "suite", module.GetOrCreateSuite("suite").Name())
	require.Equal(t, "module", session.GetOrCreateModule("module").Name())
}

func TestProcessRetryChildResultPass(t *testing.T) {
	result, exitCode, output := runProcessRetryChildResultFixture(t, "pass")
	require.Equal(t, 0, exitCode, output)
	require.Equal(t, processRetryStatusPass, result.Status)
	require.False(t, result.Failed)
	require.False(t, result.Skipped)
}

func TestProcessRetryChildResultFail(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "fail")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
	require.False(t, result.Skipped)
	require.False(t, result.Panic)
	require.Equal(t, "Error", result.ErrorType)
	require.Equal(t, "fixture failure", result.ErrorMessage)
	require.NotEmpty(t, result.ErrorStack)
}

func TestProcessRetryChildResultSkip(t *testing.T) {
	result, exitCode, output := runProcessRetryChildResultFixture(t, "skip")
	require.Equal(t, 0, exitCode, output)
	require.Equal(t, processRetryStatusSkip, result.Status)
	require.False(t, result.Failed)
	require.True(t, result.Skipped)
	require.Equal(t, "fixture skip", result.SkipReason)
}

func TestProcessRetryChildPublicHelpersPreserveNativeState(t *testing.T) {
	tests := []struct {
		name         string
		scenario     string
		status       processRetryStatus
		failed       bool
		skipped      bool
		errorType    string
		errorMessage string
		skipReason   string
	}{
		{name: "fail", scenario: "public_fail", status: processRetryStatusFail, failed: true, errorType: "Fail", errorMessage: "failed test"},
		{name: "fail now", scenario: "public_fail_now", status: processRetryStatusFail, failed: true, errorType: "FailNow", errorMessage: "failed test"},
		{name: "errorf", scenario: "public_errorf", status: processRetryStatusFail, failed: true, errorType: "Errorf", errorMessage: "fixture errorf"},
		{name: "fatal", scenario: "public_fatal", status: processRetryStatusFail, failed: true, errorType: "Fatal", errorMessage: "fixture fatal"},
		{name: "fatalf", scenario: "public_fatalf", status: processRetryStatusFail, failed: true, errorType: "Fatalf", errorMessage: "fixture fatalf"},
		{name: "skipf", scenario: "public_skipf", status: processRetryStatusSkip, skipped: true, skipReason: "fixture skipf"},
		{name: "skip now", scenario: "public_skip_now", status: processRetryStatusSkip, skipped: true},
		{name: "parallel", scenario: "public_parallel", status: processRetryStatusPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, exitCode, output := runProcessRetryChildResultFixture(t, tt.scenario)
			if tt.failed {
				require.NotEqual(t, 0, exitCode, output)
			} else {
				require.Equal(t, 0, exitCode, output)
			}
			require.Equal(t, tt.status, result.Status)
			require.Equal(t, tt.failed, result.Failed)
			require.Equal(t, tt.skipped, result.Skipped)
			require.False(t, result.Panic)
			require.Equal(t, tt.errorType, result.ErrorType)
			require.Equal(t, tt.errorMessage, result.ErrorMessage)
			require.Equal(t, tt.skipReason, result.SkipReason)
		})
	}
}

func TestProcessRetryChildResultPanic(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "panic")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
	require.True(t, result.Panic)
	require.Equal(t, "panic", result.ErrorType)
	require.Contains(t, result.ErrorMessage, "body panic sentinel")
	require.NotEmpty(t, result.ErrorStack)
}

func TestRecordProcessRetryPanicPreservesFirstPanic(t *testing.T) {
	first := &testExecutionMetadata{panicData: "first panic", panicStacktrace: "first stack"}
	execOpts := &executionOptions{panicExecutionMetadata: first}
	second := &testExecutionMetadata{}
	attempt := processRetryAttemptResult{Result: processRetryResult{
		Panic:        true,
		ErrorMessage: "second panic",
		ErrorStack:   "second stack",
	}}
	effective := processRetryEffectiveStatus{Failed: true, FailureKind: "test_panic"}

	recordProcessRetryPanic(execOpts, second, attempt, effective)
	require.Same(t, first, execOpts.panicExecutionMetadata)
	require.Equal(t, "first panic", execOpts.panicExecutionMetadata.panicData)
	require.Equal(t, "first stack", execOpts.panicExecutionMetadata.panicStacktrace)

	empty := &executionOptions{}
	recordProcessRetryPanic(empty, second, attempt, effective)
	require.Same(t, second, empty.panicExecutionMetadata)
	require.Equal(t, "second panic", second.panicData)
	require.Equal(t, "second stack", second.panicStacktrace)
}

func TestProcessRetryChildResultRuntimeGoexit(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "goexit")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
	require.True(t, result.Panic)
	require.Equal(t, "panic", result.ErrorType)
	require.Contains(t, result.ErrorMessage, "runtime.Goexit")
	require.NotEmpty(t, result.ErrorStack)
}

func TestProcessRetryChildResultFailedRuntimeGoexit(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "failed_goexit")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
	require.True(t, result.Panic)
	require.Equal(t, "panic", result.ErrorType)
	require.Contains(t, result.ErrorMessage, "runtime.Goexit")
	require.NotEmpty(t, result.ErrorStack)
}

func TestProcessRetryChildResultCleanupPanic(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "cleanup_panic")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
	require.True(t, result.Panic)
	require.Equal(t, "panic", result.ErrorType)
	require.Contains(t, result.ErrorMessage, "cleanup panic sentinel")
	require.NotEmpty(t, result.ErrorStack)
}

func TestProcessRetryChildResultCleanupSkip(t *testing.T) {
	result, exitCode, output := runProcessRetryChildResultFixture(t, "cleanup_skip")
	require.Equal(t, 0, exitCode, output)
	require.Equal(t, processRetryStatusSkip, result.Status)
	require.False(t, result.Failed)
	require.True(t, result.Skipped)
}

func TestProcessRetryChildResultCleanupFailNow(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "cleanup_failnow")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
}

func TestProcessRetryChildResultBodyPanicTakesPrecedenceOverCleanupPanic(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "body_and_cleanup_panic")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Panic)
	require.Contains(t, result.ErrorMessage, "body panic sentinel")
	require.NotContains(t, result.ErrorMessage, "cleanup panic sentinel")
}

func TestProcessRetryChildCleanupRunsExactlyOnce(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "cleanup-count")
	result, exitCode, output := runProcessRetryChildResultFixtureWithEnv(t, "cleanup_once", []string{
		processRetryChildCleanupCounterPathEnv + "=" + counterPath,
	})
	require.Equal(t, 0, exitCode, output)
	require.Equal(t, processRetryStatusPass, result.Status)
	count, err := os.ReadFile(counterPath)
	require.NoError(t, err)
	require.Equal(t, "x", string(count))
}

func TestProcessRetryChildResultParallelSubtestFailure(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "parallel_subtest_fail")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
}

func TestProcessRetryChildResultTopLevelParallelSubtestFailure(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "parallel_top_level_subtest_fail")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Failed)
}

func TestProcessRetryChildResultTopLevelParallel(t *testing.T) {
	result, exitCode, output := runProcessRetryChildResultFixture(t, "parallel_top_level")
	require.Equal(t, 0, exitCode, output)
	require.Equal(t, processRetryStatusPass, result.Status)
	require.False(t, result.Failed)
	require.False(t, result.Skipped)
}

func TestProcessRetryChildResultPanicMessageIsTruncated(t *testing.T) {
	result, exitCode, _ := runProcessRetryChildResultFixture(t, "panic_large")
	require.NotEqual(t, 0, exitCode)
	require.Equal(t, processRetryStatusFail, result.Status)
	require.True(t, result.Panic)
	require.LessOrEqual(t, len(result.ErrorMessage), processRetryErrorMessageMaxBytes)
	require.Contains(t, result.ErrorMessage, processRetryTruncationMarker)
	require.NotContains(t, result.ErrorMessage, "panic_large_tail_sentinel")
}

func TestProcessRetryStructuredMetadataIsTruncated(t *testing.T) {
	const tailSentinel = "structured_metadata_tail_sentinel"
	tests := []struct {
		name     string
		maxBytes int
		truncate func(string) string
	}{
		{name: "error type", maxBytes: processRetryErrorTypeMaxBytes, truncate: truncateProcessRetryErrorType},
		{name: "error message", maxBytes: processRetryErrorMessageMaxBytes, truncate: truncateProcessRetryStructuredErrorMessage},
		{name: "error stack", maxBytes: processRetryErrorStackMaxBytes, truncate: truncateProcessRetryStructuredErrorStack},
		{name: "skip reason", maxBytes: processRetrySkipReasonMaxBytes, truncate: truncateProcessRetrySkipReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := strings.Repeat("é", tt.maxBytes) + tailSentinel
			got := tt.truncate(value)
			require.LessOrEqual(t, len(got), tt.maxBytes)
			require.True(t, utf8.ValidString(got))
			require.Contains(t, got, processRetryMetadataTruncationMarker)
			require.NotContains(t, got, tailSentinel)
		})
	}
}

func TestProcessRetryStructuredMetadataFitsEncodedResultLimit(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	cfg := processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    "TestEncodedMetadataLimit",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	encodedExpansion := strings.Repeat("\x00<>&\xff", processRetryErrorStackMaxBytes)
	result := processRetryResult{
		Version:        1,
		TestName:       cfg.TestName,
		Attempt:        cfg.Attempt,
		RetryReason:    cfg.RetryReason,
		Status:         processRetryStatusFail,
		StartUnixNano:  1,
		FinishUnixNano: 2,
		DurationNanos:  1,
		Failed:         true,
		ErrorType:      truncateProcessRetryErrorType(encodedExpansion),
		ErrorMessage:   truncateProcessRetryStructuredErrorMessage(encodedExpansion),
		ErrorStack:     truncateProcessRetryStructuredErrorStack(encodedExpansion),
	}

	require.NoError(t, writeProcessRetryResultAtomically(resultPath, result))
	payload, err := os.ReadFile(resultPath)
	require.NoError(t, err)
	require.LessOrEqual(t, len(payload), processRetryResultMaxBytes)
	got, _, err := readProcessRetryResult(resultPath, cfg)
	require.NoError(t, err)
	require.True(t, utf8.ValidString(got.ErrorType))
	require.True(t, utf8.ValidString(got.ErrorMessage))
	require.True(t, utf8.ValidString(got.ErrorStack))
	require.Contains(t, got.ErrorMessage, processRetryMetadataTruncationMarker)

	skipCfg := processRetryChildConfig{
		ResultPath:  filepath.Join(dir, "skip-result.json"),
		TestName:    "TestEncodedSkipReasonLimit",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	skipResult := processRetryResult{
		Version:        1,
		TestName:       skipCfg.TestName,
		Attempt:        skipCfg.Attempt,
		RetryReason:    skipCfg.RetryReason,
		Status:         processRetryStatusSkip,
		StartUnixNano:  1,
		FinishUnixNano: 2,
		DurationNanos:  1,
		Skipped:        true,
		SkipReason:     truncateProcessRetrySkipReason(encodedExpansion),
	}
	require.NoError(t, writeProcessRetryResultAtomically(skipCfg.ResultPath, skipResult))
	payload, err = os.ReadFile(skipCfg.ResultPath)
	require.NoError(t, err)
	require.LessOrEqual(t, len(payload), processRetryResultMaxBytes)
	gotSkip, _, err := readProcessRetryResult(skipCfg.ResultPath, skipCfg)
	require.NoError(t, err)
	require.True(t, utf8.ValidString(gotSkip.SkipReason))
	require.Contains(t, gotSkip.SkipReason, processRetryMetadataTruncationMarker)
}

func TestBuildProcessRetryEnvPreservesPublicEnabled(t *testing.T) {
	cfg := processRetryChildConfig{
		ResultPath:  "/tmp/result.json",
		TestName:    "TestSelected",
		Attempt:     3,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	base := []string{
		constants.CIVisibilityEnabledEnvironmentVariable + "=parent",
		"DD_API_KEY=secret",
		constants.CIVisibilityInternalRetryProcessChild + "=false",
		constants.CIVisibilityInternalRetryProcessResultPath + "=/tmp/stale.json",
		constants.CIVisibilityInternalRetryProcessTestName + "=TestStale",
		constants.CIVisibilityInternalRetryProcessAttempt + "=1",
		constants.CIVisibilityInternalRetryProcessReason + "=stale",
	}

	got := buildProcessRetryEnv(base, cfg)
	envMap := envSliceToMap(got)
	require.Equal(t, "parent", envMap[constants.CIVisibilityEnabledEnvironmentVariable])
	require.Equal(t, "secret", envMap["DD_API_KEY"])
	require.Equal(t, "true", envMap[constants.CIVisibilityInternalRetryProcessChild])
	require.Equal(t, "/tmp/result.json", envMap[constants.CIVisibilityInternalRetryProcessResultPath])
	require.Equal(t, "TestSelected", envMap[constants.CIVisibilityInternalRetryProcessTestName])
	require.Equal(t, "3", envMap[constants.CIVisibilityInternalRetryProcessAttempt])
	require.Equal(t, constants.AutoTestRetriesRetryReason, envMap[constants.CIVisibilityInternalRetryProcessReason])
	require.Len(t, envValuesForKey(got, constants.CIVisibilityInternalRetryProcessResultPath, false), 1)
}

func TestBuildProcessRetryEnvRemovesInternalKeysCaseInsensitively(t *testing.T) {
	cfg := processRetryChildConfig{
		ResultPath:  "C:/tmp/result.json",
		TestName:    "TestSelected",
		Attempt:     2,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	base := []string{
		"dd_civisibility_internal_retry_process_child=false",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_RESULT_PATH=C:/stale.json",
		"dd_civisibility_internal_retry_process_test_name=TestStale",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_ATTEMPT=1",
		"dd_civisibility_internal_retry_process_reason=stale",
	}

	got := buildProcessRetryEnv(base, cfg)
	require.Len(t, envValuesForKey(got, constants.CIVisibilityInternalRetryProcessChild, true), 1)
	require.Len(t, envValuesForKey(got, constants.CIVisibilityInternalRetryProcessResultPath, true), 1)
	envMap := envSliceToMap(got)
	require.Equal(t, "true", envMap[constants.CIVisibilityInternalRetryProcessChild])
	require.Equal(t, "C:/tmp/result.json", envMap[constants.CIVisibilityInternalRetryProcessResultPath])
}

func TestReadProcessRetryResult(t *testing.T) {
	cfg := processRetryChildConfig{
		ResultPath:  filepath.Join(t.TempDir(), "result.json"),
		TestName:    "TestSelected",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	result := processRetryResult{
		Version:        1,
		TestName:       cfg.TestName,
		Attempt:        cfg.Attempt,
		RetryReason:    cfg.RetryReason,
		Status:         processRetryStatusPass,
		StartUnixNano:  10,
		FinishUnixNano: 20,
		DurationNanos:  10,
	}
	writeProcessRetryResultForTesting(t, cfg.ResultPath, result)

	got, timingOK, err := readProcessRetryResult(cfg.ResultPath, cfg)
	require.NoError(t, err)
	require.True(t, timingOK)
	require.Equal(t, processRetryStatusPass, got.Status)

	payload, err := json.Marshal(result)
	require.NoError(t, err)
	payload = append(payload[:len(payload)-1], []byte(`,"unknown_field":true}`)...)
	require.NoError(t, os.WriteFile(cfg.ResultPath, payload, 0o600))
	_, _, err = readProcessRetryResult(cfg.ResultPath, cfg)
	require.ErrorIs(t, err, errProcessRetryResultInvalid)

	validPayload, err := json.Marshal(result)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfg.ResultPath, append(validPayload, []byte(` {}`)...), 0o600))
	_, _, err = readProcessRetryResult(cfg.ResultPath, cfg)
	require.ErrorIs(t, err, errProcessRetryResultInvalid)

	result.TestName = "TestOther"
	writeProcessRetryResultForTesting(t, cfg.ResultPath, result)
	_, _, err = readProcessRetryResult(cfg.ResultPath, cfg)
	require.ErrorIs(t, err, errProcessRetryResultInvalid)

	result.TestName = cfg.TestName
	result.Failed = true
	writeProcessRetryResultForTesting(t, cfg.ResultPath, result)
	_, _, err = readProcessRetryResult(cfg.ResultPath, cfg)
	require.ErrorIs(t, err, errProcessRetryResultInvalid)

	_, _, err = readProcessRetryResult(filepath.Join(t.TempDir(), "missing.json"), cfg)
	require.ErrorIs(t, err, errProcessRetryResultMissing)

	result = processRetryResult{
		Version:     1,
		TestName:    cfg.TestName,
		Attempt:     cfg.Attempt,
		RetryReason: cfg.RetryReason,
		Status:      processRetryStatusNotRun,
	}
	writeProcessRetryResultForTesting(t, cfg.ResultPath, result)
	got, timingOK, err = readProcessRetryResult(cfg.ResultPath, cfg)
	require.NoError(t, err)
	require.False(t, timingOK)
	require.Equal(t, processRetryStatusNotRun, got.Status)

	for _, valid := range []processRetryResult{
		{
			Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
			Status: processRetryStatusFail, Failed: true, ErrorType: "Error", ErrorMessage: "message", ErrorStack: "stack",
		},
		{
			Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
			Status: processRetryStatusSkip, Skipped: true, SkipReason: "skip reason",
		},
	} {
		writeProcessRetryResultForTesting(t, cfg.ResultPath, valid)
		got, _, err := readProcessRetryResult(cfg.ResultPath, cfg)
		require.NoError(t, err)
		require.Equal(t, valid, got)
	}

	invalidResults := []struct {
		name   string
		result processRetryResult
	}{
		{
			name: "unknown version",
			result: processRetryResult{
				Version:     2,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusPass,
			},
		},
		{
			name: "unknown status",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      "unknown",
			},
		},
		{
			name: "pass failed mirror",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusPass,
				Failed:      true,
			},
		},
		{
			name: "pass skipped mirror",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusPass,
				Skipped:     true,
			},
		},
		{
			name: "pass skip reason",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusPass, SkipReason: "invalid",
			},
		},
		{
			name: "pass panic metadata",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusPass,
				Panic:       true,
				ErrorType:   "panic",
			},
		},
		{
			name: "skip missing mirror",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusSkip,
			},
		},
		{
			name: "skip failed mirror",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusSkip,
				Failed:      true,
				Skipped:     true,
			},
		},
		{
			name: "fail missing mirror",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusFail,
			},
		},
		{
			name: "fail message without type",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, ErrorMessage: "invalid",
			},
		},
		{
			name: "fail skip reason",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, SkipReason: "invalid",
			},
		},
		{
			name: "fail result error",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, ResultError: "invalid",
			},
		},
		{
			name: "oversized error type",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, ErrorType: strings.Repeat("x", processRetryErrorTypeMaxBytes+1),
			},
		},
		{
			name: "encoded oversized error type",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, ErrorType: strings.Repeat("\n", processRetryErrorTypeMaxBytes),
			},
		},
		{
			name: "oversized error message",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, ErrorType: "Error", ErrorMessage: strings.Repeat("x", processRetryErrorMessageMaxBytes+1),
			},
		},
		{
			name: "oversized error stack",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusFail, Failed: true, ErrorType: "Error", ErrorStack: strings.Repeat("x", processRetryErrorStackMaxBytes+1),
			},
		},
		{
			name: "oversized skip reason",
			result: processRetryResult{
				Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason,
				Status: processRetryStatusSkip, Skipped: true, SkipReason: strings.Repeat("x", processRetrySkipReasonMaxBytes+1),
			},
		},
		{
			name: "panic missing error type",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusFail,
				Failed:      true,
				Panic:       true,
			},
		},
		{
			name: "not run failed mirror",
			result: processRetryResult{
				Version:     1,
				TestName:    cfg.TestName,
				Attempt:     cfg.Attempt,
				RetryReason: cfg.RetryReason,
				Status:      processRetryStatusNotRun,
				Failed:      true,
			},
		},
	}
	for _, tt := range invalidResults {
		t.Run(tt.name, func(t *testing.T) {
			writeProcessRetryResultForTesting(t, cfg.ResultPath, tt.result)
			_, _, err := readProcessRetryResult(cfg.ResultPath, cfg)
			require.ErrorIs(t, err, errProcessRetryResultInvalid)
		})
	}

	t.Run("invalid timing keeps result", func(t *testing.T) {
		result := processRetryResult{
			Version:        1,
			TestName:       cfg.TestName,
			Attempt:        cfg.Attempt,
			RetryReason:    cfg.RetryReason,
			Status:         processRetryStatusPass,
			StartUnixNano:  20,
			FinishUnixNano: 10,
		}
		writeProcessRetryResultForTesting(t, cfg.ResultPath, result)
		got, timingOK, err := readProcessRetryResult(cfg.ResultPath, cfg)
		require.NoError(t, err)
		require.False(t, timingOK)
		require.Equal(t, processRetryStatusPass, got.Status)
	})

	t.Run("oversized json", func(t *testing.T) {
		oversized := bytes.Repeat([]byte("x"), processRetryResultMaxBytes+1)
		require.NoError(t, os.WriteFile(cfg.ResultPath, oversized, 0o600))
		_, _, err := readProcessRetryResult(cfg.ResultPath, cfg)
		require.ErrorIs(t, err, errProcessRetryResultInvalid)
	})

	t.Run("partial json", func(t *testing.T) {
		require.NoError(t, os.WriteFile(cfg.ResultPath, []byte(`{"version":1`), 0o600))
		_, _, err := readProcessRetryResult(cfg.ResultPath, cfg)
		require.ErrorIs(t, err, errProcessRetryResultInvalid)
	})
}

func TestProcessRetryValidateResultRejectsEncodedMetadataOverLimit(t *testing.T) {
	cfg := processRetryChildConfig{
		TestName:    "TestEncodedMetadataValidation",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	tests := []struct {
		name   string
		result processRetryResult
	}{
		{
			name: "error type",
			result: processRetryResult{
				Status:    processRetryStatusFail,
				Failed:    true,
				ErrorType: strings.Repeat("\n", processRetryErrorTypeMaxBytes),
			},
		},
		{
			name: "error message",
			result: processRetryResult{
				Status:       processRetryStatusFail,
				Failed:       true,
				ErrorType:    "Error",
				ErrorMessage: strings.Repeat("\n", processRetryErrorMessageMaxBytes),
			},
		},
		{
			name: "error stack",
			result: processRetryResult{
				Status:     processRetryStatusFail,
				Failed:     true,
				ErrorType:  "Error",
				ErrorStack: strings.Repeat("\n", processRetryErrorStackMaxBytes),
			},
		},
		{
			name: "skip reason",
			result: processRetryResult{
				Status:     processRetryStatusSkip,
				Skipped:    true,
				SkipReason: strings.Repeat("\n", processRetrySkipReasonMaxBytes),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.result.Version = 1
			tt.result.TestName = cfg.TestName
			tt.result.Attempt = cfg.Attempt
			tt.result.RetryReason = cfg.RetryReason

			require.ErrorIs(t, validateProcessRetryResult(tt.result, cfg), errProcessRetryResultInvalid)
		})
	}
}

func TestBuildProcessRetryArgs(t *testing.T) {
	registerProcessRetryArgTestFlags(t)
	tests := []struct {
		name       string
		args       []string
		testName   string
		currentCPU int
		timeout    time.Duration
		want       []string
		wantOK     bool
		wantReason string
	}{
		{
			name:       "empty args",
			testName:   "TestFoo",
			currentCPU: 2,
			timeout:    30 * time.Second,
			wantOK:     true,
			want:       []string{"-test.run=^TestFoo$", "-test.count=1", "-test.cpu=2", "-test.timeout=30s"},
		},
		{
			name:       "inserts before double dash boundary",
			args:       []string{"-test.v=true", "--", "-user-flag"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    5 * time.Second,
			wantOK:     true,
			want:       []string{"-test.v=true", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=1", "-test.timeout=5s", "--", "-user-flag"},
		},
		{
			name:       "inserts before non flag user arg",
			args:       []string{"-test.v=true", "user_arg", "-test.run=Ignored"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    5 * time.Second,
			wantOK:     true,
			want:       []string{"-test.v=true", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=1", "-test.timeout=5s", "user_arg", "-test.run=Ignored"},
		},
		{
			name:       "preserves subtest run selector tail",
			args:       []string{"-test.run", "TestFoo/SubA/SubB", "-test.skip=TestFoo/SubSkip"},
			testName:   "TestFoo",
			currentCPU: 4,
			timeout:    time.Minute,
			wantOK:     true,
			want:       []string{"-test.run=TestFoo/SubA/SubB", "-test.skip=TestFoo/SubSkip", "-test.count=1", "-test.cpu=4", "-test.timeout=1m0s"},
		},
		{
			name:       "preserves grouped top-level selector exactly",
			args:       []string{"-test.run=^(TestFoo|Other/Name)$/(OnlyThisSubtest)"},
			testName:   "TestFoo",
			currentCPU: 4,
			timeout:    time.Minute,
			wantOK:     true,
			want:       []string{"-test.run=^(TestFoo|Other/Name)$/(OnlyThisSubtest)", "-test.count=1", "-test.cpu=4", "-test.timeout=1m0s"},
		},
		{
			name:       "strips unsafe test flags and preserves registered custom values",
			args:       []string{"-config", "-test.timeout=30s", "-test.cpu=1,2", "-test.coverprofile", "cover.out", "-custom-bool", "user_arg"},
			testName:   "TestFoo",
			currentCPU: 2,
			timeout:    10 * time.Second,
			wantOK:     true,
			want:       []string{"-config", "-test.timeout=30s", "-custom-bool", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=2", "-test.timeout=10s", "user_arg"},
		},
		{
			name:       "preserves paniconexit and post-boundary unsafe-looking args",
			args:       []string{"-test.paniconexit0", "-test.outputdir", "out", "--", "-test.coverprofile", "user.out"},
			testName:   "TestFoo",
			currentCPU: 3,
			timeout:    2 * time.Second,
			wantOK:     true,
			want:       []string{"-test.paniconexit0", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=3", "-test.timeout=2s", "--", "-test.coverprofile", "user.out"},
		},
		{
			name: "strips unsafe output coverage profiling and fuzz internals",
			args: []string{
				"-test.testlogfile=events.log",
				"-test.gocoverdir", "gocover",
				"-test.coverprofile", "cover.out",
				"-test.outputdir=out",
				"-test.cpuprofile", "cpu.out",
				"-test.memprofile", "mem.out",
				"-test.blockprofile", "block.out",
				"-test.mutexprofile", "mutex.out",
				"-test.trace", "trace.out",
				"-test.artifacts",
				"-test.fuzzcachedir", "fuzzcache",
				"-test.fuzzworker",
				"-test.fuzztime", "1s",
				"-test.fuzzminimizetime=2s",
			},
			testName:   "TestFoo",
			currentCPU: 2,
			timeout:    3 * time.Second,
			wantOK:     true,
			want:       []string{"-test.run=^TestFoo$", "-test.count=1", "-test.cpu=2", "-test.timeout=3s"},
		},
		{
			name:       "preserves custom value flags with dash-looking values and inline values",
			args:       []string{"-config", "-looks-like-flag", "-custom-bool", "-config=inline"},
			testName:   "TestFoo",
			currentCPU: 2,
			timeout:    time.Second,
			wantOK:     true,
			want:       []string{"-config", "-looks-like-flag", "-custom-bool", "-config=inline", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=2", "-test.timeout=1s"},
		},
		{
			name:       "preserves unknown inline flags",
			args:       []string{"-unknown=value"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     true,
			want:       []string{"-unknown=value", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=1", "-test.timeout=1s"},
		},
		{
			name:       "unknown flag without inline value is ambiguous",
			args:       []string{"-unregistered-config", "file.json"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     false,
			wantReason: "ambiguous_unknown_flag_value",
		},
		{
			name:       "unknown flag before dash-looking token is ambiguous",
			args:       []string{"-unregistered-config", "-maybe-value"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     false,
			wantReason: "ambiguous_unknown_flag_value",
		},
		{
			name:       "unknown flag before boundary is ambiguous",
			args:       []string{"-unregistered-config", "--", "user_arg"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     false,
			wantReason: "ambiguous_unknown_flag_value",
		},
		{
			name:       "unknown flag without value is ambiguous",
			args:       []string{"-unregistered-config"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     false,
			wantReason: "ambiguous_unknown_flag_value",
		},
		{
			name:       "shuffle on is unsupported",
			args:       []string{"-shuffle=on"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     false,
			wantReason: "unsupported_shuffle_on",
		},
		{
			name:       "deterministic shuffle is preserved",
			args:       []string{"-shuffle", "12345"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     true,
			want:       []string{"-shuffle", "12345", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=1", "-test.timeout=1s"},
		},
		{
			name:       "shuffle off and post-boundary shuffle on are preserved",
			args:       []string{"-test.shuffle=off", "user_arg", "-test.shuffle=on"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     true,
			want:       []string{"-test.shuffle=off", "-test.run=^TestFoo$", "-test.count=1", "-test.cpu=1", "-test.timeout=1s", "user_arg", "-test.shuffle=on"},
		},
		{
			name:       "last run and skip selectors win",
			args:       []string{"-run=Old", "-test.run", "TestFoo/SubA", "-skip=OldSkip", "-test.skip", "NewSkip"},
			testName:   "TestFoo",
			currentCPU: 1,
			timeout:    time.Second,
			wantOK:     true,
			want:       []string{"-test.run=TestFoo/SubA", "-test.skip=NewSkip", "-test.count=1", "-test.cpu=1", "-test.timeout=1s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, reason := buildProcessRetryArgs(tt.args, tt.testName, tt.currentCPU, tt.timeout)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantReason, reason)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBuildProcessRetryFixtureArgsInsertsSelectorBeforeBoundary(t *testing.T) {
	registerProcessRetryArgTestFlags(t)
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "double dash",
			args: []string{"-test.v=true", "--", "-user-flag"},
			want: []string{"-test.v=true", "-test.run=^TestProcessRetryChildResultFixture$", "-test.count=1", "-test.cpu=1", "-test.timeout=10m0s", "--", "-user-flag"},
		},
		{
			name: "positional argument",
			args: []string{"-test.v=true", "user-arg", "-test.run=Ignored"},
			want: []string{"-test.v=true", "-test.run=^TestProcessRetryChildResultFixture$", "-test.count=1", "-test.cpu=1", "-test.timeout=10m0s", "user-arg", "-test.run=Ignored"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, reason := buildProcessRetryFixtureArgs(tt.args, "TestProcessRetryChildResultFixture")
			require.True(t, ok, reason)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBuildProcessRetryControllerArgsInsertsSelectorBeforeBoundary(t *testing.T) {
	registerProcessRetryArgTestFlags(t)
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "replaces existing selector before double dash",
			args: []string{"-test.v=true", "-test.run=Old", "--", "-user-flag"},
			want: []string{"-test.v=true", "-test.run=New", "--", "-user-flag"},
		},
		{
			name: "inserts before positional argument",
			args: []string{"-test.timeout", "30s", "user-arg", "-test.run=Ignored"},
			want: []string{"-test.timeout", "30s", "-test.run=New", "user-arg", "-test.run=Ignored"},
		},
		{
			name: "inserts before ambiguous unknown flag",
			args: []string{"-unknown", "value"},
			want: []string{"-test.run=New", "-unknown", "value"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildTestControllerSubprocessArgs(tt.args, "New"))
		})
	}
}

func TestProcessRetryTimeoutFromArgs(t *testing.T) {
	registerProcessRetryArgTestFlags(t)
	tests := []struct {
		name string
		args []string
		want time.Duration
		ok   bool
	}{
		{name: "test timeout equals", args: []string{"-test.timeout=30s"}, want: 30 * time.Second, ok: true},
		{name: "timeout space", args: []string{"-timeout", "45s"}, want: 45 * time.Second, ok: true},
		{name: "last valid wins", args: []string{"-timeout=bad", "-test.timeout", "1m"}, want: time.Minute, ok: true},
		{name: "later zero clears positive timeout", args: []string{"-timeout=30s", "-test.timeout=0"}},
		{name: "later negative clears positive timeout", args: []string{"-timeout=30s", "-test.timeout=-1s"}},
		{name: "later positive replaces zero timeout", args: []string{"-timeout=0", "-test.timeout=45s"}, want: 45 * time.Second, ok: true},
		{name: "zero ignored", args: []string{"-timeout=0"}},
		{name: "test timeout zero ignored", args: []string{"-test.timeout=0"}},
		{name: "negative ignored", args: []string{"-timeout=-1s"}},
		{name: "after boundary ignored", args: []string{"--", "-timeout=30s"}},
		{name: "test timeout after boundary ignored", args: []string{"user_arg", "-test.timeout=30s"}},
		{name: "unknown ambiguous stops parsing", args: []string{"-unknown", "-timeout=30s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := processRetryTimeoutFromArgs(tt.args)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestProcessRetryLimiter(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")

	limiter := getProcessRetryLimiter()
	first := limiter.acquire(context.Background(), nil)
	require.Equal(t, processRetryLimiterAcquired, first.Cause)
	require.NoError(t, first.Err)
	require.NotNil(t, first.Release)

	parentDeadline := make(chan time.Time)
	close(parentDeadline)
	second := limiter.acquire(context.Background(), parentDeadline)
	require.Equal(t, processRetryLimiterParentDeadline, second.Cause)
	require.Nil(t, second.Release)

	first.Release()
	first.Release()

	third := limiter.acquire(context.Background(), nil)
	require.Equal(t, processRetryLimiterAcquired, third.Cause)
	third.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := limiter.acquire(ctx, nil)
	require.Equal(t, processRetryLimiterExternalCancel, cancelled.Cause)
	require.ErrorIs(t, cancelled.Err, context.Canceled)
	require.Nil(t, cancelled.Release)
}

func TestProcessRetryLimiterStopsQueuedAcquireOnShutdown(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")

	limiter := getProcessRetryLimiter()
	held := limiter.acquire(context.Background(), nil)
	require.Equal(t, processRetryLimiterAcquired, held.Cause)
	defer held.Release()

	shutdown := make(chan struct{})
	resultCh := make(chan processRetryLimiterAcquireResult, 1)
	go func() {
		resultCh <- limiter.acquireWithShutdown(context.Background(), nil, shutdown)
	}()
	close(shutdown)

	result := <-resultCh
	require.Equal(t, processRetryLimiterShutdown, result.Cause)
	require.ErrorIs(t, result.Err, errProcessRetryShutdown)
	require.Nil(t, result.Release)
}

func TestProcessRetryShutdownWaitsForAdmittedGroups(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	require.True(t, registerProcessRetryShutdownAction())

	shutdown, finish, err := beginProcessRetryGroup()
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NotNil(t, finish)

	beginProcessRetryShutdown()
	require.True(t, processRetryShutdownRequested(shutdown))
	require.False(t, processRetryLaunchesDisabled())
	require.False(t, waitForProcessRetryShutdownQuiescence(time.Millisecond))

	finish()
	finish()
	require.True(t, waitForProcessRetryShutdownQuiescence(time.Second))
}

func TestProcessRetryUnreapedLatchRejectsWaitingStarter(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	reapWaitEntered := make(chan struct{}, 1)
	reapTimeout := make(chan time.Time, 1)
	waitCh := make(chan error)
	attempt := &processRetryAttemptResult{}
	hooks := processRetryRunnerHooks{
		after: func(time.Duration) <-chan time.Time {
			reapWaitEntered <- struct{}{}
			return reapTimeout
		},
	}

	reapResult := make(chan error, 1)
	go func() {
		reapResult <- waitForProcessRetryReapAfterKill(hooks, waitCh, attempt)
	}()
	<-reapWaitEntered

	startCalls := atomic.Int32{}
	startResult := make(chan error, 1)
	go func() {
		_, err := startProcessRetryChild(context.Background(), nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				return nil, nil
			},
		}, &exec.Cmd{})
		startResult <- err
	}()

	reapTimeout <- time.Now()
	require.ErrorIs(t, <-reapResult, errProcessRetryChildUnreaped)
	require.ErrorIs(t, <-startResult, errProcessRetryLaunchDisabled)
	require.True(t, attempt.Unreaped)
	require.True(t, processRetryLaunchesDisabled())
	require.Zero(t, startCalls.Load())
}

func TestProcessRetryReapWaitsRunConcurrently(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()

	reapWaitEntered := make(chan struct{}, 2)
	neverTimeout := make(chan time.Time)
	hooks := processRetryRunnerHooks{
		after: func(time.Duration) <-chan time.Time {
			reapWaitEntered <- struct{}{}
			return neverTimeout
		},
	}
	waitCh1 := make(chan error, 1)
	waitCh2 := make(chan error, 1)
	result1 := make(chan error, 1)
	result2 := make(chan error, 1)
	go func() { result1 <- waitForProcessRetryReapAfterKill(hooks, waitCh1, &processRetryAttemptResult{}) }()
	go func() { result2 <- waitForProcessRetryReapAfterKill(hooks, waitCh2, &processRetryAttemptResult{}) }()

	for range 2 {
		select {
		case <-reapWaitEntered:
		case <-time.After(time.Second):
			t.Fatal("process retry reap waits were serialized")
		}
	}

	started := make(chan struct{}, 1)
	startResult := make(chan error, 1)
	go func() {
		_, err := startProcessRetryChild(context.Background(), nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				started <- struct{}{}
				return make(chan error), nil
			},
		}, &exec.Cmd{})
		startResult <- err
	}()

	waitCh1 <- nil
	require.NoError(t, <-result1)
	waitCh2 <- nil
	require.NoError(t, <-result2)
	<-started
	require.NoError(t, <-startResult)
}

func TestProcessRetryLaunchWaitsWhileKillIsBlocked(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()

	timeout := make(chan time.Time)
	close(timeout)
	graceExpired := make(chan time.Time)
	close(graceExpired)
	waitCh := make(chan error, 1)
	killEntered := make(chan struct{})
	releaseKill := make(chan struct{})
	attempt := &processRetryAttemptResult{}
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- waitProcessRetryChild(
			context.Background(),
			processRetryRunnerHooks{
				terminateTree: func(*exec.Cmd) error { return nil },
				killTree: func(*exec.Cmd) error {
					close(killEntered)
					<-releaseKill
					return nil
				},
				after: func(d time.Duration) <-chan time.Time {
					if d == processRetryKillGracePeriod {
						return graceExpired
					}
					return make(chan time.Time)
				},
			},
			&exec.Cmd{},
			waitCh,
			&processRetryStaticTimer{ch: timeout},
			attempt,
		)
	}()
	<-killEntered

	processRetryLaunchGate.mu.Lock()
	reaping := processRetryLaunchGate.reaping
	processRetryLaunchGate.mu.Unlock()
	require.Equal(t, 1, reaping)

	starterContext := &processRetryObservedDoneContext{
		Context: context.Background(),
		entered: make(chan struct{}),
	}
	startCalls := atomic.Int32{}
	startResult := make(chan error, 1)
	go func() {
		_, err := startProcessRetryChild(starterContext, nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				return make(chan error), nil
			},
		}, &exec.Cmd{})
		startResult <- err
	}()
	<-starterContext.entered
	require.Zero(t, startCalls.Load())

	waitCh <- nil
	close(releaseKill)
	require.NoError(t, <-waitResult)
	require.NoError(t, <-startResult)
	require.Equal(t, int32(1), startCalls.Load())
}

func TestProcessRetryWaitResultStartsTeardownBeforeCallerCleanup(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()

	waitCh := make(chan error, 1)
	waitCh <- nil
	teardownPhase := &processRetryReapPhase{}
	callerBlocked := make(chan struct{})
	allowCallerCleanup := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		err := waitProcessRetryChildWithTeardown(
			context.Background(),
			nil,
			processRetryRunnerHooks{},
			&exec.Cmd{},
			waitCh,
			&processRetryStaticTimer{ch: make(chan time.Time)},
			&processRetryAttemptResult{},
			teardownPhase,
			func(error) {},
		)
		close(callerBlocked)
		<-allowCallerCleanup
		teardownPhase.finish(true)
		firstDone <- err
	}()
	<-callerBlocked

	processRetryLaunchGate.mu.Lock()
	reaping := processRetryLaunchGate.reaping
	processRetryLaunchGate.mu.Unlock()
	require.Equal(t, 1, reaping)

	starterContext := &processRetryObservedDoneContext{
		Context: context.Background(),
		entered: make(chan struct{}),
	}
	startCalls := atomic.Int32{}
	secondDone := make(chan error, 1)
	go func() {
		_, err := startProcessRetryChild(starterContext, nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				return make(chan error), nil
			},
		}, &exec.Cmd{})
		secondDone <- err
	}()
	<-starterContext.entered
	require.Zero(t, startCalls.Load())

	close(allowCallerCleanup)
	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, errProcessRetryLaunchDisabled)
	require.Zero(t, startCalls.Load())
}

func TestProcessRetrySuccessfulStartRegistersActiveChildBeforeReturning(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldChildren := processRetryActiveChildren.children
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.children = make(map[*exec.Cmd]processRetryActiveChild)
	processRetryActiveChildren.closeActionRegistered = true
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.children = oldChildren
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	cmd := &exec.Cmd{}
	waitCh := make(chan error)
	gotWaitCh, err := startProcessRetryChild(context.Background(), nil, processRetryRunnerHooks{
		startAndWait: func(*exec.Cmd) (<-chan error, error) { return waitCh, nil },
		killTree:     func(*exec.Cmd) error { return nil },
		killDirect:   func(*exec.Cmd) error { return nil },
	}, cmd)
	require.NoError(t, err)
	require.Equal(t, (<-chan error)(waitCh), gotWaitCh)
	processRetryActiveChildren.mu.Lock()
	_, registered := processRetryActiveChildren.children[cmd]
	shutdownRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.mu.Unlock()
	require.True(t, registered)
	require.True(t, shutdownRegistered)
	unregisterActiveProcessRetryChild(cmd)
}

func TestProcessRetryShutdownDoesNotBlockBehindInFlightStart(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldChildren := processRetryActiveChildren.children
	processRetryActiveChildren.children = make(map[*exec.Cmd]processRetryActiveChild)
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.children = oldChildren
		processRetryActiveChildren.mu.Unlock()
	})

	startEntered := make(chan struct{})
	allowStart := make(chan struct{})
	waitCh := make(chan error, 1)
	waitCh <- nil
	cmd := &exec.Cmd{}
	result := make(chan struct {
		wait <-chan error
		err  error
	}, 1)
	go func() {
		wait, err := startProcessRetryChild(context.Background(), nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				close(startEntered)
				<-allowStart
				return waitCh, nil
			},
			killTree:   func(*exec.Cmd) error { return nil },
			killDirect: func(*exec.Cmd) error { return nil },
		}, cmd)
		result <- struct {
			wait <-chan error
			err  error
		}{wait: wait, err: err}
	}()
	<-startEntered

	shutdownDone := make(chan struct{})
	go func() {
		beginProcessRetryShutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked behind an in-flight process start")
	}
	close(allowStart)
	started := <-result
	require.Equal(t, (<-chan error)(waitCh), started.wait)
	require.ErrorIs(t, started.err, errProcessRetryShutdown)
	unregisterActiveProcessRetryChild(cmd)
	require.True(t, waitForProcessRetryShutdownQuiescence(time.Second))
}

func TestProcessRetryInFlightStartRejectsContainmentLoss(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldChildren := processRetryActiveChildren.children
	processRetryActiveChildren.children = make(map[*exec.Cmd]processRetryActiveChild)
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.children = oldChildren
		processRetryActiveChildren.mu.Unlock()
	})

	startEntered := make(chan struct{})
	allowStart := make(chan struct{})
	waitCh := make(chan error, 1)
	waitCh <- nil
	cmd := &exec.Cmd{}
	result := make(chan struct {
		wait <-chan error
		err  error
	}, 1)
	go func() {
		wait, err := startProcessRetryChild(context.Background(), nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				close(startEntered)
				<-allowStart
				return waitCh, nil
			},
			killTree:   func(*exec.Cmd) error { return nil },
			killDirect: func(*exec.Cmd) error { return nil },
		}, cmd)
		result <- struct {
			wait <-chan error
			err  error
		}{wait: wait, err: err}
	}()
	<-startEntered

	reapPhase := beginProcessRetryReapPhase()
	reapPhase.finish(true)
	close(allowStart)
	started := <-result

	require.Equal(t, (<-chan error)(waitCh), started.wait)
	require.ErrorIs(t, started.err, errProcessRetryLaunchDisabled)
	require.True(t, processRetryLaunchesDisabled())
	unregisterActiveProcessRetryChild(cmd)
	require.True(t, waitForProcessRetryShutdownQuiescence(time.Second))
}

func TestRunProcessRetryAttemptStopsActiveChildOnShutdown(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.closeActionRegistered = true
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	shutdown := make(chan struct{})
	started := make(chan struct{})
	waitCh := make(chan error, 1)
	terminateCalls := atomic.Int32{}
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		closeProcessRetryCommandWriters(cmd)
		close(started)
		return waitCh, nil
	}
	hooks.terminateTree = func(*exec.Cmd) error {
		terminateCalls.Add(1)
		waitCh <- nil
		return nil
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	attemptResult := make(chan processRetryAttemptResult, 1)
	go func() {
		attemptResult <- runProcessRetryAttemptWithBaselineAndShutdown(
			context.Background(),
			processRetryChildConfig{
				TestName:    "TestShutdownActiveChild",
				Attempt:     1,
				RetryReason: constants.AutoTestRetriesRetryReason,
			},
			time.Time{},
			false,
			captureProcessRetryLaunchBaseline(),
			shutdown,
		)
	}()
	<-started
	close(shutdown)
	attempt := <-attemptResult
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}

	require.ErrorIs(t, attempt.Err, errProcessRetryShutdown)
	require.False(t, attempt.TimedOut)
	require.Equal(t, int32(1), terminateCalls.Load())
	require.Equal(t, "process_shutdown", effectiveProcessRetryStatus(attempt, false).FailureKind)
}

func TestProcessRetryStartErrorRechecksExpiredDeadline(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.closeActionRegistered = true
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	deadline := make(chan time.Time)
	startErr := errors.New("start failed after deadline")
	_, err := startProcessRetryChild(context.Background(), deadline, processRetryRunnerHooks{
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			close(deadline)
			return nil, startErr
		},
	}, &exec.Cmd{})
	require.ErrorIs(t, err, errProcessRetryLaunchDeadline)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, startErr)
}

func TestRunProcessRetryAttemptStartErrorAfterTimeoutIsTerminal(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.closeActionRegistered = true
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	timeout := make(chan time.Time)
	startErr := errors.New("start failed after process timeout")
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.startAndWait = func(*exec.Cmd) (<-chan error, error) {
		close(timeout)
		return nil, startErr
	}
	hooks.newTimer = func(time.Duration) processRetryTimer { return &processRetryStaticTimer{ch: timeout} }
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestStartErrorAfterTimeout",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}
	require.True(t, attempt.SetupFailure)
	require.True(t, attempt.SetupFallbackAllowed)
	require.True(t, attempt.TimedOut)
	require.ErrorIs(t, attempt.Err, errProcessRetryLaunchDeadline)
	require.ErrorIs(t, attempt.Err, context.DeadlineExceeded)
	require.ErrorIs(t, attempt.Err, startErr)
}

func TestProcessRetryCleanupFailureLogDoesNotExposePathOrError(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.closeActionRegistered = true
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	logger := &processRetryRecordingLogger{}
	restoreLogger := internalLog.UseLogger(logger)
	defer restoreLogger()
	oldLevel := internalLog.GetLevel()
	internalLog.SetLevel(internalLog.LevelDebug)
	defer internalLog.SetLevel(oldLevel)

	const errorSentinel = "cleanup-error-secret-sentinel"
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.removeAll = func(path string) error { return fmt.Errorf("%s:%s", errorSentinel, path) }
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestCleanupFailurePrivacy",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	attempt.Cleanup()
	internalLog.Flush()

	messages := logger.Messages()
	require.Contains(t, messages, "civisibility: process retry cleanup failed")
	require.NotContains(t, messages, attempt.TempDir)
	require.NotContains(t, messages, errorSentinel)
}

func TestProcessRetryTeardownGateRemainsHeldThroughTreeRelease(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()

	releaseEntered := make(chan struct{})
	allowRelease := make(chan struct{})
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.releaseTree = func(*exec.Cmd) error {
		close(releaseEntered)
		<-allowRelease
		return nil
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	firstDone := make(chan processRetryAttemptResult, 1)
	go func() {
		firstDone <- runProcessRetryAttempt(context.Background(), processRetryChildConfig{
			TestName:    "TestTeardownGateFirst",
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
		}, time.Time{}, false)
	}()
	<-releaseEntered

	starterContext := &processRetryObservedDoneContext{
		Context: context.Background(),
		entered: make(chan struct{}),
	}
	startCalls := atomic.Int32{}
	secondDone := make(chan error, 1)
	go func() {
		_, err := startProcessRetryChild(starterContext, nil, processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				waitCh := make(chan error, 1)
				waitCh <- nil
				return waitCh, nil
			},
		}, &exec.Cmd{})
		secondDone <- err
	}()
	<-starterContext.entered
	require.Zero(t, startCalls.Load())

	close(allowRelease)
	first := <-firstDone
	if first.Cleanup != nil {
		defer first.Cleanup()
	}
	require.NoError(t, first.Err)
	require.NoError(t, <-secondDone)
	require.Equal(t, int32(1), startCalls.Load())
}

func TestProcessRetryStopActiveChildrenStartsShutdownAndKillsOnce(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldChildren := processRetryActiveChildren.children
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.children = make(map[*exec.Cmd]processRetryActiveChild)
	processRetryActiveChildren.closeActionRegistered = false
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.children = oldChildren
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	treeKills := atomic.Int32{}
	directKills := atomic.Int32{}
	cmd := &exec.Cmd{}
	registerActiveProcessRetryChild(cmd, processRetryRunnerHooks{
		killTree: func(cmd *exec.Cmd) error {
			treeKills.Add(1)
			unregisterActiveProcessRetryChild(cmd)
			return nil
		},
		killDirect: func(*exec.Cmd) error {
			directKills.Add(1)
			return nil
		},
	})
	defer unregisterActiveProcessRetryChild(cmd)

	stopActiveProcessRetryChildren()
	stopActiveProcessRetryChildren()

	require.True(t, processRetryShuttingDown())
	require.False(t, processRetryLaunchesDisabled())
	require.Equal(t, int32(1), treeKills.Load())
	require.Equal(t, int32(1), directKills.Load())
	processRetryActiveChildren.mu.Lock()
	require.Empty(t, processRetryActiveChildren.children)
	processRetryActiveChildren.mu.Unlock()
}

func TestProcessRetryUnreapedChildRetainsShutdownOwnershipUntilWaitCompletes(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	processRetryActiveChildren.mu.Lock()
	oldChildren := processRetryActiveChildren.children
	oldRegistered := processRetryActiveChildren.closeActionRegistered
	processRetryActiveChildren.children = make(map[*exec.Cmd]processRetryActiveChild)
	processRetryActiveChildren.closeActionRegistered = true
	processRetryActiveChildren.mu.Unlock()
	t.Cleanup(func() {
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.children = oldChildren
		processRetryActiveChildren.closeActionRegistered = oldRegistered
		processRetryActiveChildren.mu.Unlock()
	})

	timeout := make(chan time.Time)
	waitCh := make(chan error, 1)
	closedTimer := make(chan time.Time)
	close(closedTimer)
	var startedCmd *exec.Cmd
	directKills := atomic.Int32{}
	removeCalls := atomic.Int32{}
	removed := make(chan struct{})
	treeKillErr := errors.New("tree kill failed")
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return treeKillErr })
	hooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		startedCmd = cmd
		closeProcessRetryCommandWriters(cmd)
		close(timeout)
		return waitCh, nil
	}
	hooks.killDirect = func(*exec.Cmd) error {
		directKills.Add(1)
		return nil
	}
	hooks.removeAll = func(string) error {
		if removeCalls.Add(1) == 1 {
			close(removed)
		}
		return nil
	}
	hooks.after = func(time.Duration) <-chan time.Time { return closedTimer }
	hooks.newTimer = func(time.Duration) processRetryTimer { return &processRetryStaticTimer{ch: timeout} }
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestUnreapedOwnership",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	attempt.Cleanup()
	require.Zero(t, removeCalls.Load())
	require.True(t, attempt.Unreaped)
	require.ErrorIs(t, attempt.Err, errProcessRetryChildUnreaped)
	require.ErrorIs(t, attempt.Err, treeKillErr)
	require.NotNil(t, startedCmd)
	require.Positive(t, directKills.Load())
	processRetryActiveChildren.mu.Lock()
	_, registered := processRetryActiveChildren.children[startedCmd]
	processRetryActiveChildren.mu.Unlock()
	require.True(t, registered)
	beginProcessRetryShutdown()
	require.False(t, waitForProcessRetryShutdownQuiescence(time.Millisecond))

	waitCh <- nil
	select {
	case <-removed:
	case <-time.After(time.Second):
		t.Fatal("unreaped process retry cleanup did not run after Wait completed")
	}
	require.Equal(t, int32(1), removeCalls.Load())
	require.Eventually(t, func() bool {
		processRetryActiveChildren.mu.Lock()
		defer processRetryActiveChildren.mu.Unlock()
		_, ok := processRetryActiveChildren.children[startedCmd]
		return !ok
	}, time.Second, time.Millisecond)
	require.True(t, waitForProcessRetryShutdownQuiescence(time.Second))
}

func TestRunProcessRetryAttemptRechecksCancellationAfterLaunchGateWait(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	releaseGate := holdProcessRetryLaunchGateForTesting(t)

	ctx, cancel := context.WithCancel(context.Background())
	conditionTriggered := make(chan struct{})
	armCondition := atomic.Bool{}
	startCalls := atomic.Int32{}
	base := time.Unix(1_700_000_000, 0)
	baseline := &processRetryLaunchBaseline{
		hooks: processRetryRunnerHooks{
			command: exec.Command,
			prepareTree: func(*exec.Cmd) error {
				armCondition.Store(true)
				return nil
			},
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				return nil, nil
			},
			releaseTree: noopProcessRetryTree,
			now: func() time.Time {
				if armCondition.CompareAndSwap(true, false) {
					cancel()
					close(conditionTriggered)
				}
				return base
			},
		},
		executable:       os.Args[0],
		workingDirectory: ".",
		timeout:          time.Second,
		timeoutSet:       true,
	}
	attemptResult := make(chan processRetryAttemptResult, 1)
	go func() {
		attemptResult <- runProcessRetryAttemptWithBaseline(ctx, processRetryChildConfig{
			TestName:    "TestCancellationAfterLaunchGateWait",
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
		}, time.Time{}, false, baseline)
	}()

	<-conditionTriggered
	releaseGate()

	attempt := <-attemptResult
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}
	require.True(t, attempt.SetupFailure)
	require.False(t, attempt.SetupFallbackAllowed)
	require.False(t, attempt.TimedOut)
	require.ErrorIs(t, attempt.Err, errProcessRetryLaunchCanceled)
	require.ErrorIs(t, attempt.Err, context.Canceled)
	require.Zero(t, startCalls.Load())
}

func TestRunProcessRetryAttemptRechecksParentDeadlineHardCapAfterLaunchGateWait(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	releaseGate := holdProcessRetryLaunchGateForTesting(t)

	base := time.Unix(1_700_000_000, 0)
	parentDeadline := base.Add(processRetryParentDeadlineReserve() + time.Minute)
	parentDeadlineHardCap := make(chan time.Time, 1)
	neverParentDeadline := make(chan time.Time)
	timerCalls := atomic.Int32{}
	conditionTriggered := make(chan struct{})
	waitingContext := &processRetryNthDoneContext{
		Context: context.Background(),
		entered: conditionTriggered,
		target:  2,
	}
	startCalls := atomic.Int32{}
	baseline := &processRetryLaunchBaseline{
		hooks: processRetryRunnerHooks{
			command:     exec.Command,
			prepareTree: func(*exec.Cmd) error { return nil },
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				return nil, nil
			},
			releaseTree: noopProcessRetryTree,
			now:         func() time.Time { return base },
			newTimer: func(time.Duration) processRetryTimer {
				if timerCalls.Add(1) == 1 {
					return &processRetryStaticTimer{ch: neverParentDeadline}
				}
				return &processRetryStaticTimer{ch: parentDeadlineHardCap}
			},
		},
		executable:       os.Args[0],
		workingDirectory: ".",
		timeout:          time.Second,
		timeoutSet:       true,
	}
	attemptResult := make(chan processRetryAttemptResult, 1)
	go func() {
		attemptResult <- runProcessRetryAttemptWithBaseline(waitingContext, processRetryChildConfig{
			TestName:    "TestParentDeadlineAfterLaunchGateWait",
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
		}, parentDeadline, true, baseline)
	}()

	<-conditionTriggered
	parentDeadlineHardCap <- base
	releaseGate()

	attempt := <-attemptResult
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}
	require.True(t, attempt.SetupFailure)
	require.True(t, attempt.SetupFallbackAllowed)
	require.True(t, attempt.TimedOut)
	require.ErrorIs(t, attempt.Err, errProcessRetryLaunchDeadline)
	require.ErrorIs(t, attempt.Err, context.DeadlineExceeded)
	require.Zero(t, startCalls.Load())
}

func holdProcessRetryLaunchGateForTesting(t *testing.T) func() {
	t.Helper()
	reapWaitEntered := make(chan struct{}, 1)
	reapTimeout := make(chan time.Time)
	waitCh := make(chan error, 1)
	reapResult := make(chan error, 1)
	go func() {
		reapResult <- waitForProcessRetryReapAfterKill(processRetryRunnerHooks{
			after: func(time.Duration) <-chan time.Time {
				reapWaitEntered <- struct{}{}
				return reapTimeout
			},
		}, waitCh, &processRetryAttemptResult{})
	}()
	<-reapWaitEntered

	return func() {
		waitCh <- nil
		require.NoError(t, <-reapResult)
	}
}

func TestProcessRetryReapPrefersObservedExitAtTimeoutBoundary(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	for range 32 {
		waitCh := make(chan error, 1)
		waitCh <- nil
		timeoutCh := make(chan time.Time, 1)
		timeoutCh <- time.Now()
		attempt := &processRetryAttemptResult{}

		err := waitForProcessRetryReapAfterKill(processRetryRunnerHooks{
			after: func(time.Duration) <-chan time.Time { return timeoutCh },
		}, waitCh, attempt)

		require.NoError(t, err)
		require.False(t, attempt.Unreaped)
		require.False(t, processRetryLaunchesDisabled())
	}
}

func TestAttemptFromWaitErrorPreservesCancellationEvidenceWithExitError(t *testing.T) {
	attempt := processRetryAttemptResult{ExitCode: processRetryExitCodeUnset}
	attemptFromWaitError(&attempt, errors.Join(context.Canceled, &exec.ExitError{}))

	require.ErrorIs(t, attempt.Err, context.Canceled)
	require.True(t, attempt.ExitStatusObserved)
	require.Equal(t, "process_canceled", effectiveProcessRetryStatus(attempt, false).FailureKind)
}

func TestRunProcessRetryAttemptHonorsConcurrencyCap(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")

	started := make(chan string, 2)
	releaseChildren := make(chan struct{})
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			if cmd.Stdin != nil {
				return nil, errors.New("process retry child inherited stdin")
			}
			cfg, err := parseProcessRetryChildConfigFromCommandEnv(cmd.Env)
			if err != nil {
				return nil, err
			}
			now := time.Now()
			result := processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			}
			data, err := json.Marshal(result)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(cfg.ResultPath, data, 0o600); err != nil {
				return nil, err
			}
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			started <- cfg.TestName
			waitCh := make(chan error, 1)
			go func() {
				<-releaseChildren
				waitCh <- nil
			}()
			return waitCh, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	firstDone := make(chan processRetryAttemptResult, 1)
	secondDone := make(chan processRetryAttemptResult, 1)
	go func() {
		firstDone <- runProcessRetryAttempt(context.Background(), processRetryChildConfig{
			TestName:    "TestProcessRetryConcurrentOne",
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
		}, time.Time{}, false)
	}()
	require.Equal(t, "TestProcessRetryConcurrentOne", <-started)

	acquireEntered := make(chan struct{})
	allowAcquire := make(chan struct{})
	secondContext := &processRetryBlockingDoneContext{
		Context: context.Background(),
		entered: acquireEntered,
		release: allowAcquire,
	}
	go func() {
		secondDone <- runProcessRetryAttempt(secondContext, processRetryChildConfig{
			TestName:    "TestProcessRetryConcurrentTwo",
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
		}, time.Time{}, false)
	}()
	<-acquireEntered
	require.Len(t, started, 0)
	close(allowAcquire)

	close(releaseChildren)
	require.Equal(t, "TestProcessRetryConcurrentTwo", <-started)
	first := <-firstDone
	second := <-secondDone
	defer func() {
		if first.Cleanup != nil {
			first.Cleanup()
		}
		if second.Cleanup != nil {
			second.Cleanup()
		}
	}()
	require.NoError(t, first.Err)
	require.NoError(t, second.Err)
	require.Equal(t, processRetryStatusPass, first.Result.Status)
	require.Equal(t, processRetryStatusPass, second.Result.Status)
}

func TestProcessRetryBoundedOutput(t *testing.T) {
	sink, err := newProcessRetryBoundedOutput(t.TempDir(), "stdout", 4)
	require.NoError(t, err)
	n, err := sink.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.NoError(t, sink.Close())

	tail, truncated, err := sink.Tail()
	require.NoError(t, err)
	require.True(t, truncated)
	require.Equal(t, "cdef", tail)
	info, err := os.Stat(sink.path)
	require.NoError(t, err)
	require.LessOrEqual(t, info.Size(), int64(4))
}

func TestProcessRetryBoundedOutputWriteAfterClose(t *testing.T) {
	sink, err := newProcessRetryBoundedOutput(t.TempDir(), "stdout", 16)
	require.NoError(t, err)
	_, err = sink.Write([]byte("before"))
	require.NoError(t, err)
	require.NoError(t, sink.Close())

	beforeTail, beforeTruncated, beforeErr := sink.Tail()
	n, err := sink.Write([]byte("after"))
	require.NoError(t, err)
	require.Equal(t, len("after"), n)
	afterTail, afterTruncated, afterErr := sink.Tail()

	require.Equal(t, beforeTail, afterTail)
	require.Equal(t, beforeTruncated, afterTruncated)
	require.Equal(t, beforeErr, afterErr)
}

func TestCombineProcessRetryOutputTailsMarksPerStreamTruncation(t *testing.T) {
	sink, err := newProcessRetryBoundedOutput(t.TempDir(), "stdout", 4)
	require.NoError(t, err)
	_, err = sink.Write([]byte("prefix-tail"))
	require.NoError(t, err)
	require.NoError(t, sink.Close())

	combined, truncated, err := combineProcessRetryOutputTails(&processRetryOutputCapture{sink: sink}, nil, 16)
	require.NoError(t, err)
	require.True(t, truncated)
	require.Equal(t, 1, strings.Count(combined, processRetryOutputTruncationMarker))
	require.Equal(t, processRetryOutputTruncationMarker+"tail", combined)
}

func TestProcessRetryOutputCaptureAbortIsIdempotent(t *testing.T) {
	capture, err := newProcessRetryOutputCapture(t.TempDir(), "stdout", processRetryStreamFileMaxBytes)
	require.NoError(t, err)
	capture.StartCopy()

	firstErr := capture.AbortAfterReapedChild(time.Second)
	secondErr := capture.AbortAfterReapedChild(time.Nanosecond)

	require.Equal(t, fmt.Sprint(firstErr), fmt.Sprint(secondErr))
	require.NotContains(t, fmt.Sprint(secondErr), "abort timed out")
}

func TestFinalizeProcessRetryOutputCapturesKillsTreeWithinSingleDrainBudget(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	tempDir := t.TempDir()
	stdoutCapture, err := newProcessRetryOutputCapture(tempDir, "stdout", processRetryStreamFileMaxBytes)
	require.NoError(t, err)
	stderrCapture, err := newProcessRetryOutputCapture(tempDir, "stderr", processRetryStreamFileMaxBytes)
	require.NoError(t, err)
	stdoutCapture.StartCopy()
	stderrCapture.StartCopy()

	killCalls := atomic.Int32{}
	hooks := processRetryRunnerHooks{
		killTree: func(*exec.Cmd) error {
			killCalls.Add(1)
			return nil
		},
	}
	attempt := processRetryAttemptResult{
		Result:   processRetryResult{Status: processRetryStatusPass},
		ExitCode: 0,
	}
	started := time.Now()
	finalizeProcessRetryOutputCaptures(hooks, &exec.Cmd{}, &attempt, stdoutCapture, stderrCapture)
	elapsed := time.Since(started)

	require.Less(t, elapsed, 2*processRetryOutputDrainWait)
	require.Equal(t, int32(1), killCalls.Load())
	require.ErrorIs(t, attempt.CaptureErr, errProcessRetryOutputDrainTimedOut)
	require.ErrorIs(t, attempt.Err, errProcessRetryContainmentLost)
	require.True(t, attempt.ContainmentLost)
	require.True(t, attempt.OutputTruncated)
	effective := effectiveProcessRetryStatus(attempt, false)
	require.Equal(t, processRetryStatusFail, effective.Status)
	require.True(t, effective.Failed)
	require.Equal(t, "containment_lost", effective.FailureKind)
}

func TestRunProcessRetryAttemptContainsOrdinaryDescendant(t *testing.T) {
	requireProcessRetryContainmentForTesting(t)
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()

	readyPath := filepath.Join(t.TempDir(), "ordinary-descendant-ready")
	t.Setenv("Bypass", "true")
	t.Setenv(processRetryChildResultScenarioEnv, processRetryOrdinaryDescendantScenario)
	t.Setenv(processRetryOrdinaryDescendantReadyPathEnv, readyPath)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		payload, err := os.ReadFile(readyPath)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(payload)))
		if err != nil || pid <= 0 {
			return
		}
		process, err := os.FindProcess(pid)
		if err == nil {
			_ = process.Kill()
		}
	})

	baseline := captureProcessRetryLaunchBaseline()
	require.NoError(t, baseline.err)
	baseline.argsSnapshot.runSelector = ""
	baseline.argsSnapshot.skipSelector = ""
	attempt := runProcessRetryAttemptWithBaseline(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryChildResultFixture",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false, baseline)
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}

	require.False(t, attempt.SetupFailure)
	require.NoError(t, attempt.Err)
	require.NoError(t, attempt.CaptureErr)
	require.False(t, attempt.Unreaped)
	require.Equal(t, 0, attempt.ExitCode)
	require.Equal(t, processRetryStatusPass, attempt.Result.Status)
	require.Contains(t, attempt.OutputTail, "ordinary descendant stdout ready")
	require.Contains(t, attempt.OutputTail, "ordinary descendant stderr ready")
	require.FileExists(t, readyPath)
	require.False(t, processRetryLaunchesDisabled())
	effective := effectiveProcessRetryStatus(attempt, false)
	require.Equal(t, processRetryStatusPass, effective.Status)
	require.False(t, effective.Failed)
}

func TestProcessRetryUnitRunFilterIncludesSpecialCaseRegressions(t *testing.T) {
	testNames := []string{
		"TestFinalizeProcessRetryOutputCapturesKillsTreeWithinSingleDrainBudget",
		"TestCombineProcessRetryOutputTailsMarksPerStreamTruncation",
		"TestRecordProcessRetryPanicPreservesFirstPanic",
	}
	tests := make([]testing.InternalTest, 0, len(testNames))
	for _, testName := range testNames {
		tests = append(tests, testing.InternalTest{Name: testName})
	}
	filter := buildProcessRetryUnitRunFilter(tests)
	for _, testName := range testNames {
		matched, err := regexp.MatchString(filter, testName)
		require.NoError(t, err)
		require.Truef(t, matched, "%s is excluded from the normal package test run", testName)
	}
}

func TestRunProcessRetryAttemptSetsCommandEnv(t *testing.T) {
	requireProcessRetryContainmentForTesting(t)
	resetProcessRetryLimiterForTesting(t)
	runnerHooks := defaultProcessRetryRunnerHooks()
	runnerHooks.args = func() []string { return nil }
	resetProcessRetryRunnerHooksForTesting(t, runnerHooks)
	t.Setenv(processRetryChildResultScenarioEnv, "pass")
	cfg := processRetryChildConfig{
		TestName:    "TestProcessRetryChildResultFixture",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}

	attempt := runProcessRetryAttempt(context.Background(), cfg, time.Time{}, false)
	defer func() {
		if attempt.Cleanup != nil {
			attempt.Cleanup()
		}
	}()

	require.False(t, attempt.SetupFailure)
	require.NoError(t, attempt.Err)
	require.Equal(t, 0, attempt.ExitCode)
	require.Equal(t, processRetryStatusPass, attempt.Result.Status)
	require.Equal(t, cfg.TestName, attempt.Result.TestName)
	require.Equal(t, cfg.Attempt, attempt.Result.Attempt)
	require.Equal(t, cfg.RetryReason, attempt.Result.RetryReason)
	require.NotEmpty(t, attempt.TempDir)
	require.FileExists(t, filepath.Join(attempt.TempDir, "result.json"))
	require.FileExists(t, attempt.StdoutPath)
	require.FileExists(t, attempt.StderrPath)
	requireProcessRetryFileMode(t, attempt.TempDir, 0o700)
	requireProcessRetryFileMode(t, filepath.Join(attempt.TempDir, "result.json"), 0o600)
	requireProcessRetryFileMode(t, attempt.StdoutPath, 0o600)
	requireProcessRetryFileMode(t, attempt.StderrPath, 0o600)
}

func TestRunProcessRetryAttemptDoesNotInheritStdin(t *testing.T) {
	requireProcessRetryContainmentForTesting(t)
	resetProcessRetryLimiterForTesting(t)
	runnerHooks := defaultProcessRetryRunnerHooks()
	runnerHooks.args = func() []string { return nil }
	resetProcessRetryRunnerHooksForTesting(t, runnerHooks)
	t.Setenv(processRetryChildResultScenarioEnv, "stdin_eof")
	cfg := processRetryChildConfig{
		TestName:    "TestProcessRetryChildResultFixture",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}

	attempt := runProcessRetryAttempt(context.Background(), cfg, time.Time{}, false)
	defer func() {
		if attempt.Cleanup != nil {
			attempt.Cleanup()
		}
	}()

	require.False(t, attempt.SetupFailure)
	require.NoError(t, attempt.Err)
	require.Equal(t, 0, attempt.ExitCode)
	require.Equal(t, processRetryStatusPass, attempt.Result.Status)
}

func TestRunProcessRetryAttemptFallsBackWhenTreeContainmentIsUnavailable(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	startCalls := atomic.Int32{}
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		prepareTree: func(*exec.Cmd) error {
			return errProcessRetryTreeUnsupported
		},
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			return nil, errors.New("unexpected process start")
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryContainmentUnavailable",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}

	require.True(t, attempt.SetupFailure)
	require.True(t, attempt.SetupFallbackAllowed)
	require.ErrorIs(t, attempt.Err, errProcessRetryTreeUnsupported)
	require.Zero(t, startCalls.Load())
}

func TestRunProcessRetryAttemptAttachesBeforeResumeAndReleasesLast(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	phases := make([]string, 0, 6)
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		prepareTree: func(*exec.Cmd) error {
			phases = append(phases, "prepare")
			return nil
		},
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			phases = append(phases, "start")
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			closeProcessRetryCommandWriters(cmd)
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		attachTree: func(*exec.Cmd) error {
			phases = append(phases, "attach")
			return nil
		},
		resumeTree: func(*exec.Cmd) error {
			phases = append(phases, "resume")
			return nil
		},
		terminateTree: func(*exec.Cmd) error { return nil },
		killTree: func(*exec.Cmd) error {
			phases = append(phases, "kill")
			return nil
		},
		killDirect: func(*exec.Cmd) error { return nil },
		releaseTree: func(*exec.Cmd) error {
			phases = append(phases, "release")
			return nil
		},
		now:   time.Now,
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryPhaseOrder",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()
	require.NoError(t, attempt.Err)
	require.Equal(t, processRetryStatusPass, attempt.Result.Status)
	require.Equal(t, []string{"prepare", "start", "attach", "resume", "kill", "release"}, phases)
}

func TestRunProcessRetryAttemptSuspendedAttachFailureFallsBackBeforeConsumption(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	attachErr := errors.New("attach failed")
	startCalls := atomic.Int32{}
	killCalls := atomic.Int32{}
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			closeProcessRetryCommandWriters(cmd)
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		attachTree: func(*exec.Cmd) error { return attachErr },
		killDirect: func(*exec.Cmd) error {
			killCalls.Add(1)
			return nil
		},
		startsSuspended: true,
	})

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestSuspendedAttachFailure",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}

	require.True(t, attempt.SetupFailure)
	require.True(t, attempt.SetupFallbackAllowed)
	require.False(t, attempt.ContainmentLost)
	require.ErrorIs(t, attempt.Err, attachErr)
	require.Equal(t, int32(1), startCalls.Load())
	require.Equal(t, int32(1), killCalls.Load())
	require.False(t, processRetryLaunchesDisabled())
}

func TestRunProcessRetryAttemptRunningAttachFailureIsTerminal(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	attachErr := errors.New("attach failed after launch")
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			closeProcessRetryCommandWriters(cmd)
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		attachTree:      func(*exec.Cmd) error { return attachErr },
		killDirect:      func(*exec.Cmd) error { return nil },
		startsSuspended: false,
	})

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestRunningAttachFailure",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}

	require.True(t, attempt.SetupFailure)
	require.False(t, attempt.SetupFallbackAllowed)
	require.True(t, attempt.ContainmentLost)
	require.ErrorIs(t, attempt.Err, attachErr)
	require.ErrorIs(t, attempt.Err, errProcessRetryContainmentLost)
	require.True(t, processRetryLaunchesDisabled())
}

func TestRunProcessRetryAttemptStartLatencyConsumesParentDeadlineBeforeResume(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	now := time.Unix(1_700_000_000, 0)
	resumeCalls := atomic.Int32{}
	killCalls := atomic.Int32{}
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		prepareTree:      func(*exec.Cmd) error { return nil },
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			now = now.Add(20 * time.Millisecond)
			closeProcessRetryCommandWriters(cmd)
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		attachTree: func(*exec.Cmd) error { return nil },
		resumeTree: func(*exec.Cmd) error {
			resumeCalls.Add(1)
			return nil
		},
		terminateTree: func(*exec.Cmd) error { return nil },
		killTree: func(*exec.Cmd) error {
			killCalls.Add(1)
			return nil
		},
		killDirect:  func(*exec.Cmd) error { return nil },
		releaseTree: func(*exec.Cmd) error { return nil },
		now:         func() time.Time { return now },
		after:       time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	parentDeadline := now.Add(processRetryParentDeadlineReserve() + 10*time.Millisecond)
	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryStartDeadline",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, parentDeadline, true)
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()
	require.False(t, attempt.SetupFailure)
	require.True(t, attempt.TimedOut)
	require.Zero(t, resumeCalls.Load())
	require.Positive(t, killCalls.Load())
}

func TestRunProcessRetryAttemptStartsConfiguredTimeoutBeforeChildLaunch(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable, "30s")
	now := time.Unix(1_700_000_000, 0)
	var timerDuration time.Duration
	timerCh := make(chan time.Time, 1)
	killCalls := atomic.Int32{}
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.now = func() time.Time { return now }
	originalStart := hooks.startAndWait
	hooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		now = now.Add(20 * time.Second)
		timerCh <- now
		return originalStart(cmd)
	}
	hooks.killTree = func(*exec.Cmd) error {
		killCalls.Add(1)
		return nil
	}
	hooks.newTimer = func(d time.Duration) processRetryTimer {
		timerDuration = d
		return &processRetryStaticTimer{ch: timerCh}
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryConfiguredTimeout",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()
	require.True(t, attempt.TimedOut)
	require.Positive(t, killCalls.Load())
	require.Equal(t, 30*time.Second, timerDuration)
}

func TestRunProcessRetryAttemptPropagatesPostExitTreeCleanupFailure(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	treeErr := errors.New("tree cleanup failed")
	resetProcessRetryRunnerHooksForTesting(t, processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error {
		return treeErr
	}))

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryTreeCleanupFailure",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()
	require.ErrorIs(t, attempt.Err, treeErr)
	effective := effectiveProcessRetryStatus(attempt, false)
	require.Equal(t, "containment_lost", effective.FailureKind)
	require.True(t, processRetryLaunchesDisabled())
}

func TestRunProcessRetryAttemptPropagatesTreeReleaseFailure(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	releaseErr := errors.New("tree release failed")
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.releaseTree = func(*exec.Cmd) error { return releaseErr }
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	attempt := runProcessRetryAttempt(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryTreeReleaseFailure",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()
	require.ErrorIs(t, attempt.Err, releaseErr)
	require.Equal(t, "containment_lost", effectiveProcessRetryStatus(attempt, false).FailureKind)
	require.True(t, processRetryLaunchesDisabled())
}

func TestProcessRetryWaitPropagatesTerminateFailure(t *testing.T) {
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	terminateErr := errors.New("tree terminate failed")
	timeoutCh := make(chan time.Time, 1)
	timeoutCh <- time.Now()
	after := func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	attempt := processRetryAttemptResult{}
	waitErr := waitProcessRetryChild(
		context.Background(),
		processRetryRunnerHooks{
			terminateTree: func(*exec.Cmd) error { return terminateErr },
			killTree:      func(*exec.Cmd) error { return nil },
			after:         after,
		},
		&exec.Cmd{},
		make(chan error),
		&processRetryStaticTimer{ch: timeoutCh},
		&attempt,
	)
	require.ErrorIs(t, attempt.Err, terminateErr)
	require.ErrorIs(t, waitErr, errProcessRetryChildUnreaped)
}

func TestRunProcessRetryAttemptHonorsParentDeadlineWhileWaitingForLimiter(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")
	held := getProcessRetryLimiter().acquire(context.Background(), nil)
	require.Equal(t, processRetryLimiterAcquired, held.Cause)
	require.NotNil(t, held.Release)
	defer held.Release()

	startCalls := atomic.Int32{}
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	cfg := processRetryChildConfig{
		TestName:    "TestProcessRetryDeadline",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	parentDeadline := time.Now().Add(processRetryParentDeadlineReserve() + 10*time.Millisecond)
	attempt := runProcessRetryAttempt(context.Background(), cfg, parentDeadline, true)
	require.True(t, attempt.SetupFailure)
	require.True(t, attempt.SetupFallbackAllowed)
	require.True(t, attempt.TimedOut)
	require.Empty(t, attempt.TempDir)
	require.Empty(t, attempt.StdoutPath)
	require.Empty(t, attempt.StderrPath)
	require.Equal(t, int32(0), startCalls.Load())
}

func TestRunProcessRetryAttemptStartsProcessTimeoutAfterLimiterAcquire(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")
	t.Setenv(constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable, "20ms")
	held := getProcessRetryLimiter().acquire(context.Background(), nil)
	require.Equal(t, processRetryLimiterAcquired, held.Cause)
	require.NotNil(t, held.Release)
	defer held.Release()

	startCalls := atomic.Int32{}
	timerDurations := make(chan time.Duration, 1)
	timerCh := make(chan time.Time)
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			cfg, err := parseProcessRetryChildConfigFromCommandEnv(cmd.Env)
			if err != nil {
				return nil, err
			}
			now := time.Now()
			data, err := json.Marshal(processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(cfg.ResultPath, data, 0o600); err != nil {
				return nil, err
			}
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			timerDurations <- d
			return &processRetryStaticTimer{ch: timerCh}
		},
	})

	acquireEntered := make(chan struct{})
	allowAcquire := make(chan struct{})
	waitingContext := &processRetryBlockingDoneContext{
		Context: context.Background(),
		entered: acquireEntered,
		release: allowAcquire,
	}
	done := make(chan processRetryAttemptResult, 1)
	go func() {
		done <- runProcessRetryAttempt(waitingContext, processRetryChildConfig{
			TestName:    "TestProcessRetryLimiterTimeout",
			Attempt:     1,
			RetryReason: constants.AutoTestRetriesRetryReason,
		}, time.Time{}, false)
	}()

	<-acquireEntered
	require.Equal(t, int32(0), startCalls.Load())
	require.Len(t, timerDurations, 0)
	close(allowAcquire)
	held.Release()
	require.Equal(t, 20*time.Millisecond, <-timerDurations)

	attempt := <-done
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()
	require.False(t, attempt.SetupFailure)
	require.NoError(t, attempt.Err)
	require.Equal(t, processRetryStatusPass, attempt.Result.Status)
	require.Equal(t, int32(1), startCalls.Load())
}

func TestRunProcessRetryAttemptChecksCancellationImmediatelyBeforeStart(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	ctx, cancel := context.WithCancel(context.Background())
	startCalls := atomic.Int32{}
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command: func(executable string, args ...string) *exec.Cmd {
			cancel()
			return exec.Command(executable, args...)
		},
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			return nil, errors.New("unexpected child start")
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	attempt := runProcessRetryAttempt(ctx, processRetryChildConfig{
		TestName:    "TestProcessRetryCancelledBeforeStart",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false)
	require.NotNil(t, attempt.Cleanup)
	defer attempt.Cleanup()

	require.True(t, attempt.SetupFailure)
	require.False(t, attempt.SetupFallbackAllowed)
	require.ErrorIs(t, attempt.Err, context.Canceled)
	require.Equal(t, int32(0), startCalls.Load())
	require.NotEmpty(t, attempt.TempDir)
	require.NotEmpty(t, attempt.StdoutPath)
	require.NotEmpty(t, attempt.StderrPath)
}

func TestRunTestWithRetryUsesProcessBackendForRetries(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	childCalls := atomic.Int32{}
	preExecCalls := atomic.Int32{}
	preProcessCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			childCalls.Add(1)
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_, _ = stdout.Write([]byte("child stdout\n"))
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	identity := newTestIdentity("module", "suite", "TestProcessRetryBackend")
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	addModulesCounters(identity.ModuleName, 1)
	addSuitesCounters(identity.SuiteName, 1)
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	options.preExecMetaAdjust = func(execMeta *testExecutionMetadata, _ int) {
		preExecCalls.Add(1)
		execMeta.identity = identity
		execMeta.isFlakyTestRetriesEnabled = true
	}
	options.preProcessRetryMetaAdjust = func(execMeta *testExecutionMetadata, _ int) {
		preProcessCalls.Add(1)
		execMeta.identity = identity
		execMeta.isFlakyTestRetriesEnabled = true
	}
	runTestWithRetry(options)
	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(1), childCalls.Load())
	require.Equal(t, int32(1), preExecCalls.Load())
	require.Equal(t, int32(2), preProcessCalls.Load())
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusPass, recorder.tests[0].status)
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
	require.Contains(t, recorder.tests[0].logs, "child stdout")
	module := recorder.modules[identity.ModuleName]
	require.NotNil(t, module)
	suite := module.suites[identity.SuiteName]
	require.NotNil(t, suite)
	checkModuleAndSuite(module, suite)
	require.Equal(t, 1, suite.closeCount)
	require.Equal(t, 1, module.closeCount)
	require.Zero(t, suitesCounters[identity.SuiteName])
	require.Zero(t, modulesCounters[identity.ModuleName])
}

func TestRunTestWithRetryRuntimeGoexitRetriesInProcess(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "in_process")
	defer restoreEnv()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	identity := newTestIdentity("module", "suite", "TestRuntimeGoexitInProcess")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	var bodyCalls atomic.Int32
	options := processRetryRunOptionsForTesting(t, identity, func(*testing.T) {
		if bodyCalls.Add(1) == 1 {
			runtime.Goexit()
		}
	})

	runTestWithRetry(options)

	require.Equal(t, int32(2), bodyCalls.Load())
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestRunTestWithRetryFailedRuntimeGoexitUsesPanicSemanticsInProcess(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "in_process")
	defer restoreEnv()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	identity := newTestIdentity("module", "suite", "TestFailedRuntimeGoexitInProcess")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	var bodyCalls atomic.Int32
	var firstPanic any
	options := processRetryRunOptionsForTesting(t, identity, func(localT *testing.T) {
		if bodyCalls.Add(1) == 1 {
			localT.Fail()
			runtime.Goexit()
		}
	})
	options.postPerExecution = func(_ *testing.T, execMeta *testExecutionMetadata, executionIndex int, _ time.Duration) {
		if executionIndex == 0 {
			firstPanic = execMeta.panicData
		}
	}

	runTestWithRetry(options)

	require.Equal(t, int32(2), bodyCalls.Load())
	require.Equal(t, unexpectedTestTerminationMessage, firstPanic)
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestRunTestWithRetryRuntimeGoexitUsesProcessRetry(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	})
	defer restoreSupport()

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	var bodyCalls atomic.Int32
	var childCalls atomic.Int32
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	startAndWait := hooks.startAndWait
	hooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		childCalls.Add(1)
		return startAndWait(cmd)
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	identity := newTestIdentity("module", "suite", "TestRuntimeGoexitProcess")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	options := processRetryRunOptionsForTesting(t, identity, func(*testing.T) {
		bodyCalls.Add(1)
		runtime.Goexit()
	})

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(1), childCalls.Load())
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusPass, recorder.tests[0].status)
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
}

func TestRunTestWithRetryUsesPreFirstAttemptLaunchBaseline(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreConcurrency := setEnvForTesting(t, constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")
	defer restoreConcurrency()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()
	_, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	executable := "baseline-executable"
	workingDirectory := "baseline-directory"
	fs := useIsolatedProcessRetryFlagSet(t)
	fs.String("custom", "", "baseline value flag")
	args := []string{"-custom", "baseline"}
	environment := []string{"BASELINE_ENV=1"}
	baselineCPU := runtime.GOMAXPROCS(0)
	contaminatedCPU := 1
	if baselineCPU == contaminatedCPU {
		contaminatedCPU = 2
	}
	defer runtime.GOMAXPROCS(baselineCPU)
	coverageActive := atomic.Bool{}
	fuzzActive := atomic.Bool{}
	executableCalls := atomic.Int32{}
	workingDirectoryCalls := atomic.Int32{}
	argsCalls := atomic.Int32{}
	environmentCalls := atomic.Int32{}

	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable: func() (string, error) {
			executableCalls.Add(1)
			return executable, nil
		},
		workingDirectory: func() (string, error) {
			workingDirectoryCalls.Add(1)
			return workingDirectory, nil
		},
		args: func() []string {
			argsCalls.Add(1)
			return args
		},
		environ: func() []string {
			environmentCalls.Add(1)
			return environment
		},
		command:     exec.Command,
		prepareTree: func(*exec.Cmd) error { return nil },
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			require.Equal(t, "baseline-executable", cmd.Path)
			require.Equal(t, "baseline-directory", cmd.Dir)
			require.Contains(t, cmd.Args, "-custom")
			require.Contains(t, cmd.Args, "baseline")
			require.Contains(t, cmd.Args, "-test.cpu="+strconv.Itoa(baselineCPU))
			require.NotContains(t, cmd.Args, "contaminated")
			require.Equal(t, 1, cap(getProcessRetryLimiter().ch))
			envMap := envSliceToMap(cmd.Env)
			require.Equal(t, "1", envMap["BASELINE_ENV"])
			require.NotContains(t, envMap, "CONTAMINATED_ENV")

			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		attachTree:    func(*exec.Cmd) error { return nil },
		resumeTree:    func(*exec.Cmd) error { return nil },
		terminateTree: func(*exec.Cmd) error { return nil },
		killTree:      func(*exec.Cmd) error { return nil },
		killDirect:    func(*exec.Cmd) error { return nil },
		releaseTree:   func(*exec.Cmd) error { return nil },
		after:         time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	identity := newTestIdentity("module", "suite", "TestProcessRetryLaunchBaseline")
	restoreBudget := setProcessRetryBudgetForTesting(1, 100)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	bodyCalls := atomic.Int32{}
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		if bodyCalls.Add(1) == 1 {
			executable = "contaminated-executable"
			workingDirectory = "contaminated-directory"
			args[1] = "contaminated"
			environment[0] = "CONTAMINATED_ENV=1"
			flag.CommandLine = flag.NewFlagSet("contaminated", flag.ContinueOnError)
			runtime.GOMAXPROCS(contaminatedCPU)
			coverageActive.Store(true)
			fuzzActive.Store(true)
			require.NoError(t, os.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "7"))
			require.NoError(t, os.Setenv(constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "in_process"))
			t.Fail()
		}
	})
	options.coverageActive = coverageActive.Load
	options.fuzzActive = fuzzActive.Load
	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(1), executableCalls.Load())
	require.Equal(t, int32(1), workingDirectoryCalls.Load())
	require.Equal(t, int32(1), argsCalls.Load())
	require.Equal(t, int32(1), environmentCalls.Load())
}

func TestRunTestWithRetryProcessTagParityWithInProcessRetry(t *testing.T) {
	inProcess := recordInProcessRetryTagsForParity(t)
	process := runProcessRetryParityCase(t)

	require.Equal(t, processRetryStatusPass, inProcess.status)
	require.Equal(t, processRetryStatusPass, process.status)
	require.Equal(t, "true", inProcess.tags[constants.TestIsRetry])
	require.Equal(t, "true", process.tags[constants.TestIsRetry])
	require.Equal(t, constants.AutoTestRetriesRetryReason, inProcess.tags[constants.TestRetryReason])
	require.Equal(t, constants.AutoTestRetriesRetryReason, process.tags[constants.TestRetryReason])
	require.NotContains(t, inProcess.tags, constants.TestRetryExecutionMode)
	require.Equal(t, "process", process.tags[constants.TestRetryExecutionMode])

	for key, want := range inProcess.tags {
		if key == constants.TestRetryExecutionMode {
			continue
		}
		require.Equalf(t, want, process.tags[key], "process retry tag %q differs from in-process retry", key)
	}
	for key := range process.tags {
		if key == constants.TestRetryExecutionMode {
			continue
		}
		require.Containsf(t, inProcess.tags, key, "process retry has extra non-process-specific tag %q", key)
	}
}

func TestCloseProcessRetryTestEventDoesNotChangeAggregateCounters(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	identity := newTestIdentity("module", "suite", "TestProcessRetryLifecycle")
	testInfo := &commonInfo{
		moduleName: identity.ModuleName,
		suiteName:  identity.SuiteName,
		testName:   identity.FullName,
		identity:   identity,
	}
	execMeta := &testExecutionMetadata{
		identity:                  identity,
		isARetry:                  true,
		isFlakyTestRetriesEnabled: true,
		remainingRetries:          0,
	}
	now := time.Now()
	attempt := processRetryAttemptResult{
		Result: processRetryResult{
			Status: processRetryStatusFail,
			Failed: true,
		},
		ExitCode:   1,
		StartTime:  now,
		FinishTime: now.Add(time.Millisecond),
	}

	effective := closeProcessRetryTestEvent(testInfo, execMeta, attempt)

	require.True(t, effective.Failed)
	require.Len(t, recorder.modules, 1)
	module := recorder.modules[identity.ModuleName]
	require.NotNil(t, module)
	require.Len(t, module.suites, 1)
	suite := module.suites[identity.SuiteName]
	require.NotNil(t, suite)
	require.Len(t, recorder.tests, 1)
	require.Equal(t, 1, recorder.tests[0].closeCount)
	require.Zero(t, suite.closeCount)
	require.Zero(t, module.closeCount)
	require.Zero(t, recorder.closeCount)
	require.Zero(t, modulesCounters[identity.ModuleName])
	require.Zero(t, suitesCounters[identity.SuiteName])
	require.Equal(t, true, recorder.tests[0].tags[ext.Error])
	require.Empty(t, recorder.tests[0].errorType)
	require.Empty(t, recorder.tests[0].errorMessage)
	require.Empty(t, recorder.tests[0].errorStack)
	require.Equal(t, true, suite.tags[ext.Error])
	require.Equal(t, true, module.tags[ext.Error])
}

func TestCloseProcessRetryTestEventForwardsStructuredResultMetadata(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
		defer restoreSession()

		identity := newTestIdentity("module", "suite", "TestProcessRetryStructuredFailure")
		now := time.Now()
		effective := closeProcessRetryTestEvent(&commonInfo{
			moduleName: identity.ModuleName,
			suiteName:  identity.SuiteName,
			testName:   identity.FullName,
			identity:   identity,
		}, &testExecutionMetadata{
			identity:                  identity,
			isARetry:                  true,
			isFlakyTestRetriesEnabled: true,
			isLastRetry:               true,
		}, processRetryAttemptResult{
			Result: processRetryResult{
				Status:       processRetryStatusFail,
				Failed:       true,
				ErrorType:    "Error",
				ErrorMessage: "structured failure sentinel",
				ErrorStack:   "structured stack sentinel",
			},
			ExitCode:   1,
			StartTime:  now,
			FinishTime: now.Add(time.Millisecond),
		})

		require.True(t, effective.Failed)
		require.Len(t, recorder.tests, 1)
		require.Equal(t, "Error", recorder.tests[0].errorType)
		require.Equal(t, "structured failure sentinel", recorder.tests[0].errorMessage)
		require.Equal(t, "structured stack sentinel", recorder.tests[0].errorStack)
	})

	t.Run("skip", func(t *testing.T) {
		recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
		defer restoreSession()

		identity := newTestIdentity("module", "suite", "TestProcessRetryStructuredSkip")
		now := time.Now()
		effective := closeProcessRetryTestEvent(&commonInfo{
			moduleName: identity.ModuleName,
			suiteName:  identity.SuiteName,
			testName:   identity.FullName,
			identity:   identity,
		}, &testExecutionMetadata{
			identity:                  identity,
			isARetry:                  true,
			isFlakyTestRetriesEnabled: true,
			isLastRetry:               true,
		}, processRetryAttemptResult{
			Result: processRetryResult{
				Status:     processRetryStatusSkip,
				Skipped:    true,
				SkipReason: "structured skip sentinel",
			},
			ExitCode:   0,
			StartTime:  now,
			FinishTime: now.Add(time.Millisecond),
		})

		require.True(t, effective.Skipped)
		require.Len(t, recorder.tests, 1)
		require.Equal(t, "structured skip sentinel", recorder.tests[0].skipReason)
	})
}

func TestCloseProcessRetryTestEventPropagatesITRForcedRun(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	telemetryRecorder := new(telemetrytest.RecordClient)
	defer coretelemetry.MockClient(telemetryRecorder)()

	identity := newTestIdentity("module", "suite", "TestProcessRetryForcedRun")
	snapshot := snapshotProcessRetryExecutionMetadata(&testExecutionMetadata{
		identity:                  identity,
		isFlakyTestRetriesEnabled: true,
		isItrForcedRun:            true,
	})
	execMeta := &testExecutionMetadata{isARetry: true}
	require.True(t, applyProcessRetryMetadataSnapshot(execMeta, snapshot))

	now := time.Now()
	effective := closeProcessRetryTestEvent(&commonInfo{
		moduleName: identity.ModuleName,
		suiteName:  identity.SuiteName,
		testName:   identity.FullName,
		identity:   identity,
	}, execMeta, processRetryAttemptResult{
		Result:     processRetryResult{Status: processRetryStatusPass},
		ExitCode:   0,
		StartTime:  now,
		FinishTime: now.Add(time.Millisecond),
	})

	require.Equal(t, processRetryStatusPass, effective.Status)
	require.Len(t, recorder.tests, 1)
	require.Equal(t, "true", recorder.tests[0].tags[constants.TestForcedToRun])
	metric := telemetrytest.MetricKey{
		Namespace: coretelemetry.NamespaceCIVisibility,
		Name:      "itr_forced_run",
		Tags:      "event_type:test",
		Kind:      "count",
	}
	require.Contains(t, telemetryRecorder.Metrics, metric)
	require.Equal(t, 1.0, telemetryRecorder.Metrics[metric].Get())
}

func TestCloseProcessRetryTestEventKeepsOutputOutOfSpanMetadata(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	identity := newTestIdentity("module", "suite", "TestProcessRetrySensitiveOutput")
	testInfo := &commonInfo{
		moduleName: identity.ModuleName,
		suiteName:  identity.SuiteName,
		testName:   identity.FullName,
		identity:   identity,
	}
	execMeta := &testExecutionMetadata{
		identity:                  identity,
		isARetry:                  true,
		isFlakyTestRetriesEnabled: true,
		remainingRetries:          0,
	}
	secretSentinel := "DD_API_KEY=process-retry-secret-sentinel"
	pathSentinel := filepath.Join(t.TempDir(), "process-retry-path-sentinel")
	outputTail := strings.Join([]string{
		"ordinary child output",
		secretSentinel,
		pathSentinel,
	}, "\n")

	now := time.Now()
	effective := closeProcessRetryTestEvent(testInfo, execMeta, processRetryAttemptResult{
		Result: processRetryResult{
			Status: processRetryStatusFail,
			Failed: true,
		},
		ExitCode:   1,
		OutputTail: outputTail,
		StartTime:  now,
		FinishTime: now.Add(time.Millisecond),
	})

	require.True(t, effective.Failed)
	require.Len(t, recorder.tests, 1)
	require.Contains(t, recorder.tests[0].logs, secretSentinel)
	require.Contains(t, recorder.tests[0].logs, pathSentinel)

	for _, tags := range []map[string]any{
		recorder.tests[0].tags,
		recorder.modules[identity.ModuleName].tags,
		recorder.modules[identity.ModuleName].suites[identity.SuiteName].tags,
	} {
		requireProcessRetryTagsExclude(t, tags, secretSentinel, pathSentinel)
	}
}

func TestCloseProcessRetryTestEventForwardsOutputForEffectiveStatuses(t *testing.T) {
	tests := []struct {
		name    string
		result  processRetryResult
		attempt func(processRetryAttemptResult) processRetryAttemptResult
	}{
		{
			name: "fail",
			result: processRetryResult{
				Status: processRetryStatusFail,
				Failed: true,
			},
		},
		{
			name: "skip",
			result: processRetryResult{
				Status:  processRetryStatusSkip,
				Skipped: true,
			},
		},
		{
			name: "timeout",
			result: processRetryResult{
				Status: processRetryStatusPass,
			},
			attempt: func(attempt processRetryAttemptResult) processRetryAttemptResult {
				attempt.TimedOut = true
				return attempt
			},
		},
		{
			name: "cancellation",
			result: processRetryResult{
				Status: processRetryStatusPass,
			},
			attempt: func(attempt processRetryAttemptResult) processRetryAttemptResult {
				attempt.Err = context.Canceled
				return attempt
			},
		},
		{
			name: "panic",
			result: processRetryResult{
				Status:       processRetryStatusFail,
				Failed:       true,
				Panic:        true,
				ErrorType:    "panic",
				ErrorMessage: "panic sentinel",
				ErrorStack:   "stack sentinel",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
			defer restoreSession()

			identity := newTestIdentity("module", "suite", "TestProcessRetryOutput"+tt.name)
			testInfo := &commonInfo{
				moduleName: identity.ModuleName,
				suiteName:  identity.SuiteName,
				testName:   identity.FullName,
				identity:   identity,
			}
			execMeta := &testExecutionMetadata{
				identity:                  identity,
				isARetry:                  true,
				isFlakyTestRetriesEnabled: true,
				remainingRetries:          0,
			}
			now := time.Now()
			attempt := processRetryAttemptResult{
				Result:     tt.result,
				ExitCode:   0,
				OutputTail: "process retry " + tt.name + " output sentinel",
				StartTime:  now,
				FinishTime: now.Add(time.Millisecond),
			}
			if tt.result.Status == processRetryStatusFail {
				attempt.ExitCode = 1
			}
			if tt.attempt != nil {
				attempt = tt.attempt(attempt)
			}

			closeProcessRetryTestEvent(testInfo, execMeta, attempt)

			require.Len(t, recorder.tests, 1)
			require.Contains(t, recorder.tests[0].logs, attempt.OutputTail)
		})
	}
}

func TestProcessRetryDiagnosticsKeepSecretPathSentinelsOutOfSpanMetadata(t *testing.T) {
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	secretSentinel := "process-retry-env-secret-sentinel"
	customSecretSentinel := "process-retry-custom-secret-sentinel"
	homePathSentinel := filepath.Join(t.TempDir(), "home", "process-retry-path-sentinel")
	workspacePathSentinel := filepath.Join(t.TempDir(), "workspace", "process-retry-path-sentinel")
	tempPathSentinel := filepath.Join(t.TempDir(), "tmp", "process-retry-path-sentinel")
	for _, pair := range [][2]string{
		{constants.APIKeyEnvironmentVariable, secretSentinel},
		{"PROCESS_RETRY_CUSTOM_SECRET_SENTINEL", customSecretSentinel},
		{"PROCESS_RETRY_HOME_PATH_SENTINEL", homePathSentinel},
		{"PROCESS_RETRY_WORKSPACE_PATH_SENTINEL", workspacePathSentinel},
		{"PROCESS_RETRY_TEMP_PATH_SENTINEL", tempPathSentinel},
	} {
		t.Setenv(pair[0], pair[1])
	}
	forbidden := []string{secretSentinel, customSecretSentinel, homePathSentinel, workspacePathSentinel, tempPathSentinel}

	cfg := processRetryChildConfig{
		ResultPath:  filepath.Join(t.TempDir(), "result.json"),
		TestName:    "TestProcessRetrySensitiveDiagnostics",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
		Version:     1,
		TestName:    secretSentinel,
		Attempt:     cfg.Attempt,
		RetryReason: workspacePathSentinel,
		Status:      processRetryStatusPass,
	})
	_, _, err := readProcessRetryResult(cfg.ResultPath, cfg)
	require.ErrorIs(t, err, errProcessRetryResultInvalid)
	for _, sentinel := range forbidden {
		require.NotContains(t, err.Error(), sentinel)
	}

	identity := newTestIdentity("module", "suite", cfg.TestName)
	testInfo := &commonInfo{
		moduleName: identity.ModuleName,
		suiteName:  identity.SuiteName,
		testName:   identity.FullName,
		identity:   identity,
	}
	execMeta := &testExecutionMetadata{
		identity:                  identity,
		isARetry:                  true,
		isFlakyTestRetriesEnabled: true,
		remainingRetries:          0,
	}
	effective := closeProcessRetryTestEvent(testInfo, execMeta, processRetryAttemptResult{
		Err:        fmt.Errorf("%w: %s %s", errProcessRetryResultInvalid, secretSentinel, tempPathSentinel),
		ExitCode:   processRetryExitCodeUnset,
		StartTime:  time.Now(),
		FinishTime: time.Now().Add(time.Millisecond),
	})
	require.True(t, effective.Failed)
	require.Len(t, recorder.tests, 1)
	require.Empty(t, recorder.tests[0].logs)
	for _, tags := range []map[string]any{
		recorder.tests[0].tags,
		recorder.modules[identity.ModuleName].tags,
		recorder.modules[identity.ModuleName].suites[identity.SuiteName].tags,
	} {
		requireProcessRetryTagsExclude(t, tags, forbidden...)
	}
}

func TestRunTestWithRetryProcessExternalCancellationNoFallback(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	startCalls := atomic.Int32{}
	postShouldRetryCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	identity := newTestIdentity("module", "suite", "TestProcessRetryCancellation")
	restoreBudget := setProcessRetryBudgetForTesting(1, 100)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	options.processRetryContext = func() context.Context { return cancelled }
	options.postShouldRetry = func(*testing.T, *testExecutionMetadata, int, int64) bool {
		postShouldRetryCalls.Add(1)
		return true
	}

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(0), startCalls.Load())
	require.Equal(t, int32(1), postShouldRetryCalls.Load())
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
}

func TestRunTestWithRetryProcessCancellationAfterStartIsTerminal(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	resetProcessRetryLimiterForTesting(t)
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startCalls := atomic.Int32{}
	var waitCh chan error
	resetProcessRetryRunnerHooksForTesting(t, processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			waitCh = make(chan error, 1)
			cancel()
			return waitCh, nil
		},
		terminateTree: func(cmd *exec.Cmd) error {
			closeProcessRetryCommandWriters(cmd)
			waitCh <- &exec.ExitError{}
			return nil
		},
		now:   time.Now,
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	})

	restoreBudget := setProcessRetryBudgetForTesting(2, 2)
	defer restoreBudget()
	identity := newTestIdentity("module", "suite", "TestProcessRetryCancellationAfterStart")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	bodyCalls := atomic.Int32{}
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	options.processRetryContext = func() context.Context { return ctx }
	options.postAdjustRetryCount = func(*testExecutionMetadata, time.Duration) int64 { return 2 }

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(1), startCalls.Load())
	require.Len(t, recorder.tests, 1)
	require.Equal(t, "process_canceled", recorder.tests[0].errorType)
	require.Equal(t, int64(1), atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestRunTestWithRetryParentDeadlineWhileQueuedStopsFurtherRetries(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreConcurrency := setEnvForTesting(t, constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")
	defer restoreConcurrency()
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	childCalls := atomic.Int32{}
	timerCalls := atomic.Int32{}
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		childCalls.Add(1)
		cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
		now := time.Now()
		writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
			Version:        1,
			TestName:       cfg.TestName,
			Attempt:        cfg.Attempt,
			RetryReason:    cfg.RetryReason,
			Status:         processRetryStatusFail,
			Failed:         true,
			StartUnixNano:  now.UnixNano(),
			FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
			DurationNanos:  int64(time.Millisecond),
		})
		closeProcessRetryCommandWriters(cmd)
		waitCh := make(chan error, 1)
		waitCh <- nil
		return waitCh, nil
	}
	hooks.newTimer = func(time.Duration) processRetryTimer {
		ch := make(chan time.Time, 1)
		if timerCalls.Add(1) == 3 {
			ch <- time.Now()
		}
		return &processRetryStaticTimer{ch: ch}
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	identity := newTestIdentity("module", "suite", "TestProcessRetryQueuedDeadline")
	restoreBudget := setProcessRetryBudgetForTesting(3, 3)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) { t.Fail() })
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 3)
	originalPostPerExecution := options.postPerExecution
	var held processRetryLimiterAcquireResult
	options.postPerExecution = func(localT *testing.T, execMeta *testExecutionMetadata, executionIndex int, duration time.Duration) {
		originalPostPerExecution(localT, execMeta, executionIndex, duration)
		if executionIndex == 1 {
			held = getProcessRetryLimiter().acquire(context.Background(), nil)
			require.Equal(t, processRetryLimiterAcquired, held.Cause)
		}
	}

	runTestWithRetry(options)
	if held.Release != nil {
		held.Release()
	}

	require.Equal(t, int32(1), childCalls.Load())
	require.Equal(t, int64(1), atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
	require.Len(t, recorder.tests, 2)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, "timeout", recorder.tests[1].errorType)
}

func TestRunProcessRetryAttemptFallsBackWhenLaunchesAreDisabled(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	disableProcessRetryLaunches()

	startCalls := atomic.Int32{}
	baseline := &processRetryLaunchBaseline{
		hooks: processRetryRunnerHooks{
			startAndWait: func(*exec.Cmd) (<-chan error, error) {
				startCalls.Add(1)
				return nil, nil
			},
		},
		executable:       os.Args[0],
		workingDirectory: ".",
		timeout:          time.Second,
		timeoutSet:       true,
	}
	attempt := runProcessRetryAttemptWithBaseline(context.Background(), processRetryChildConfig{
		TestName:    "TestProcessRetryDisabledLaunch",
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}, time.Time{}, false, baseline)

	require.True(t, attempt.SetupFailure)
	require.True(t, attempt.SetupFallbackAllowed)
	require.ErrorIs(t, attempt.Err, errProcessRetryLaunchDisabled)
	require.Zero(t, startCalls.Load())
	require.Empty(t, attempt.TempDir)
}

func TestRunTestWithRetryShutdownQueuedBeforeLimiterIsTerminal(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	t.Setenv(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable, "1")
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	held := getProcessRetryLimiter().acquire(context.Background(), nil)
	require.Equal(t, processRetryLimiterAcquired, held.Cause)
	defer held.Release()

	startCalls := atomic.Int32{}
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.startAndWait = func(*exec.Cmd) (<-chan error, error) {
		startCalls.Add(1)
		return nil, errors.New("process retry launched after shutdown")
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	identity := newTestIdentity("module", "suite", "TestProcessRetryQueuedShutdown")
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	bodyCalls := atomic.Int32{}
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 1)
	postShouldRetryCalls := atomic.Int32{}
	originalPostShouldRetry := options.postShouldRetry
	options.postShouldRetry = func(localT *testing.T, execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool {
		postShouldRetryCalls.Add(1)
		return originalPostShouldRetry(localT, execMeta, executionIndex, remainingRetries)
	}
	observed := &processRetryObservedDoneContext{
		Context: context.Background(),
		entered: make(chan struct{}),
	}
	options.processRetryContext = func() context.Context { return observed }

	done := make(chan struct{})
	go func() {
		runTestWithRetry(options)
		close(done)
	}()
	select {
	case <-observed.entered:
	case <-done:
		t.Fatalf(
			"process retry finished before reaching the limiter: body_calls=%d start_calls=%d post_should_retry_calls=%d remaining_budget=%d registered=%t shutting_down=%t disabled=%t",
			bodyCalls.Load(),
			startCalls.Load(),
			postShouldRetryCalls.Load(),
			atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount),
			processRetryShutdownActionRegistered(),
			processRetryShuttingDown(),
			processRetryLaunchesDisabled(),
		)
	case <-time.After(time.Second):
		t.Fatal("process retry did not reach the limiter")
	}
	beginProcessRetryShutdown()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queued process retry did not stop after shutdown")
	}

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Zero(t, startCalls.Load())
	require.True(t, processRetryShuttingDown())
	require.False(t, processRetryLaunchesDisabled())
	require.True(t, waitForProcessRetryShutdownQuiescence(time.Second))
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, "process_shutdown", recorder.tests[0].errorType)
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
}

func TestRunTestWithRetryShutdownPreventsSetupFallbackIteration(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	startCalls := atomic.Int32{}
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	hooks.prepareTree = func(*exec.Cmd) error {
		beginProcessRetryShutdown()
		return errProcessRetryTreeUnsupported
	}
	hooks.startAndWait = func(*exec.Cmd) (<-chan error, error) {
		startCalls.Add(1)
		return nil, errors.New("process retry launched after shutdown")
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	identity := newTestIdentity("module", "suite", "TestProcessRetryShutdownFallback")
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	bodyCalls := atomic.Int32{}
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 1)

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Zero(t, startCalls.Load())
	require.True(t, processRetryShuttingDown())
	require.False(t, processRetryLaunchesDisabled())
	require.True(t, waitForProcessRetryShutdownQuiescence(time.Second))
	require.Equal(t, int64(1), atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
	require.Empty(t, recorder.tests)
}

func TestRunTestWithRetryUnreapedChildStopsFurtherProcessRetries(t *testing.T) {
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	startCalls := atomic.Int32{}
	postShouldRetryCalls := atomic.Int32{}
	var timeoutCh chan time.Time
	readyTime := func() <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	runnerHooks := processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			closeProcessRetryCommandWriters(cmd)
			timeoutCh <- time.Now()
			return make(chan error), nil
		},
		terminateTree: func(*exec.Cmd) error { return nil },
		killTree:      func(*exec.Cmd) error { return nil },
		killDirect:    func(*exec.Cmd) error { return nil },
		after:         func(time.Duration) <-chan time.Time { return readyTime() },
		newTimer: func(time.Duration) processRetryTimer {
			timeoutCh = make(chan time.Time, 1)
			return &processRetryStaticTimer{ch: timeoutCh}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	identity := newTestIdentity("module", "suite", "TestProcessRetryUnreaped")
	restoreBudget := setProcessRetryBudgetForTesting(3, 3)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 3)
	originalPostShouldRetry := options.postShouldRetry
	options.postShouldRetry = func(t *testing.T, execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool {
		postShouldRetryCalls.Add(1)
		return originalPostShouldRetry(t, execMeta, executionIndex, remainingRetries)
	}

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(1), startCalls.Load())
	require.Equal(t, int32(1), postShouldRetryCalls.Load())
	require.True(t, processRetryLaunchesDisabled())
	require.Equal(t, int64(2), atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, "process_unreaped", recorder.tests[0].errorType)
	require.Equal(t, constants.TestStatusFail, recorder.tests[0].tags[constants.TestFinalStatus])
	require.Equal(t, "true", recorder.tests[0].tags[constants.TestHasFailedAllRetries])
}

func TestRunTestWithRetryContainmentLossStopsFurtherProcessRetries(t *testing.T) {
	resetProcessRetryLimiterForTesting(t)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	childCalls := atomic.Int32{}
	treeErr := errors.New("containment cleanup failed")
	hooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return treeErr })
	hooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		childCalls.Add(1)
		cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
		now := time.Now()
		writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
			Version:        1,
			TestName:       cfg.TestName,
			Attempt:        cfg.Attempt,
			RetryReason:    cfg.RetryReason,
			Status:         processRetryStatusFail,
			Failed:         true,
			StartUnixNano:  now.UnixNano(),
			FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
			DurationNanos:  int64(time.Millisecond),
		})
		closeProcessRetryCommandWriters(cmd)
		waitCh := make(chan error, 1)
		waitCh <- nil
		return waitCh, nil
	}
	resetProcessRetryRunnerHooksForTesting(t, hooks)

	identity := newTestIdentity("module", "suite", "TestProcessRetryContainmentLoss")
	restoreBudget := setProcessRetryBudgetForTesting(3, 3)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) { t.Fail() })
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 3)

	runTestWithRetry(options)

	require.Equal(t, int32(1), childCalls.Load())
	require.True(t, processRetryLaunchesDisabled())
	require.Equal(t, int64(2), atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
	require.Len(t, recorder.tests, 1)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, "containment_lost", recorder.tests[0].errorType)
	require.Equal(t, constants.TestStatusFail, recorder.tests[0].tags[constants.TestFinalStatus])
}

func TestRunTestWithRetryLaunchDisabledUsesInProcessFallbackForNewGroup(t *testing.T) {
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreLaunchGate()
	disableProcessRetryLaunches()
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	startCalls := atomic.Int32{}
	runnerHooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	runnerHooks.startAndWait = func(*exec.Cmd) (<-chan error, error) {
		startCalls.Add(1)
		return nil, errors.New("unexpected process retry launch")
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	bodyCalls := atomic.Int32{}
	identity := newTestIdentity("module", "suite", "TestProcessRetryDisabledFallback")
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		if bodyCalls.Add(1) == 1 {
			t.Fail()
		}
	})
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 1)
	require.True(t, processRetryLaunchesDisabled())

	runTestWithRetry(options)

	require.Equal(t, int32(2), bodyCalls.Load())
	require.Zero(t, startCalls.Load())
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestRunTestWithRetryFallsBackBeforeConsumingProcessAttempt(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	bodyCalls := atomic.Int32{}
	startCalls := atomic.Int32{}
	processAdjustCalls := atomic.Int32{}
	processIsLastCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return "", os.ErrNotExist },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(*exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	identity := newTestIdentity("module", "suite", "TestProcessRetryFallback")
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		if bodyCalls.Load() == 1 {
			t.Fail()
		}
	})
	options.preProcessRetryMetaAdjust = func(*testExecutionMetadata, int) {
		processAdjustCalls.Add(1)
	}
	options.preIsLastRetry = func(*testExecutionMetadata, int, int64) bool {
		processIsLastCalls.Add(1)
		return false
	}
	runTestWithRetry(options)

	require.Equal(t, int32(2), bodyCalls.Load())
	require.Equal(t, int32(0), startCalls.Load())
	require.Equal(t, int32(0), processAdjustCalls.Load())
	require.Equal(t, int32(1), processIsLastCalls.Load())
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestRunTestWithRetryIneligibleProcessModeUsesInProcessRetry(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	restoreSupport := setProcessRetrySupportHooksForTesting(t, processRetrySupportHooks{
		childCleanupSupported:      func() bool { return true },
		testingMWorkloadsSupported: func() bool { return true },
	})
	defer restoreSupport()

	startCalls := atomic.Int32{}
	runnerHooks := processRetrySuccessfulAttemptHooks(t, func(*exec.Cmd) error { return nil })
	startAndWait := runnerHooks.startAndWait
	runnerHooks.startAndWait = func(cmd *exec.Cmd) (<-chan error, error) {
		startCalls.Add(1)
		return startAndWait(cmd)
	}
	resetProcessRetryRunnerHooksForTesting(t, runnerHooks)

	bodyCalls := atomic.Int32{}
	identity := newTestIdentity("module", "suite", "TestProcessRetryIneligible")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		if bodyCalls.Add(1) == 1 {
			t.Fail()
		}
	})
	options.coverageActive = func() bool { return true }

	runTestWithRetry(options)

	require.Equal(t, int32(2), bodyCalls.Load())
	require.Zero(t, startCalls.Load())
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestRunTestWithRetryProcessSetupFailureAfterConsumed(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	startCalls := atomic.Int32{}
	executableCalls := atomic.Int32{}
	prepareCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) {
			executableCalls.Add(1)
			return os.Args[0], nil
		},
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		prepareTree: func(*exec.Cmd) error {
			if prepareCalls.Add(1) == 1 {
				return nil
			}
			return os.ErrNotExist
		},
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			startCalls.Add(1)
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusFail,
				Failed:         true,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	identity := newTestIdentity("module", "suite", "TestProcessRetrySetupFailureAfterConsumed")
	restoreBudget := setProcessRetryBudgetForTesting(2, 100)
	defer restoreBudget()
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	options.postAdjustRetryCount = func(*testExecutionMetadata, time.Duration) int64 {
		return 2
	}

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(1), startCalls.Load())
	require.Equal(t, int32(1), executableCalls.Load())
	require.Equal(t, int32(2), prepareCalls.Load())
	require.Len(t, recorder.tests, 2)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, processRetryStatusFail, recorder.tests[1].status)
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
	require.Equal(t, "process", recorder.tests[1].tags[constants.TestRetryExecutionMode])
	require.Equal(t, true, recorder.tests[1].tags["error"])
}

func TestRunTestWithRetryProcessConsumesRetryBudget(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	childCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			call := childCalls.Add(1)
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			status := processRetryStatusFail
			failed := true
			if call == 2 {
				status = processRetryStatusPass
				failed = false
			}
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         status,
				Failed:         failed,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	identity := newTestIdentity("module", "suite", "TestProcessRetryBudget")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	restoreBudget := setProcessRetryBudgetForTesting(2, 2)
	defer restoreBudget()
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	options.postAdjustRetryCount = func(*testExecutionMetadata, time.Duration) int64 {
		return 2
	}

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(2), childCalls.Load())
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
	require.Len(t, recorder.tests, 2)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, processRetryStatusPass, recorder.tests[1].status)
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
	require.Equal(t, "process", recorder.tests[1].tags[constants.TestRetryExecutionMode])
}

func TestProcessRetryReservesFlakyRetryBudgetBeforeAdmission(t *testing.T) {
	settings := integrations.GetFlakyRetriesSettings()
	oldRemaining := atomic.LoadInt64(&settings.RemainingTotalRetryCount)
	atomic.StoreInt64(&settings.RemainingTotalRetryCount, 1)
	t.Cleanup(func() {
		atomic.StoreInt64(&settings.RemainingTotalRetryCount, oldRemaining)
	})

	localT := createNewTest()
	localT.Fail()
	execMeta := &testExecutionMetadata{isFlakyTestRetriesEnabled: true}
	execOpts := &executionOptions{
		options: &runTestWithRetryOptions{
			postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool { return true },
		},
		retryCount: 1,
	}

	require.True(t, reserveRetryBudgetIfNeeded(execOpts, localT, execMeta, 0))
	require.True(t, execOpts.flakyRetryBudgetReservation.reserved())
	require.Zero(t, atomic.LoadInt64(&settings.RemainingTotalRetryCount))
}

func TestProcessRetryFlakyRetryBudgetReservationIsAtomic(t *testing.T) {
	settings := integrations.GetFlakyRetriesSettings()
	oldRetryCount := settings.RetryCount
	settings.RetryCount = 1
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer func() {
		restoreBudget()
		settings.RetryCount = oldRetryCount
	}()

	start := make(chan struct{})
	results := make(chan bool, 2)
	metadata := []*testExecutionMetadata{
		{hasAdditionalFeatureWrapper: true, isFlakyTestRetriesEnabled: true, flakyRetryBudgetReservation: &flakyRetryBudgetReservation{}},
		{hasAdditionalFeatureWrapper: true, isFlakyTestRetriesEnabled: true, flakyRetryBudgetReservation: &flakyRetryBudgetReservation{}},
	}
	for _, execMeta := range metadata {
		go func(meta *testExecutionMetadata) {
			<-start
			results <- isFinalExecution(true, false, meta, 0)
		}(execMeta)
	}
	close(start)

	finalCount := 0
	for range metadata {
		if <-results {
			finalCount++
		}
	}
	reservedCount := 0
	for _, execMeta := range metadata {
		if execMeta.flakyRetryBudgetReservation != nil && execMeta.flakyRetryBudgetReservation.reserved() {
			reservedCount++
		}
	}

	require.Equal(t, 1, finalCount)
	require.Equal(t, 1, reservedCount)
	require.Zero(t, atomic.LoadInt64(&settings.RemainingTotalRetryCount))
}

func TestProcessRetryFlakyRetryBudgetRefundIsIdempotent(t *testing.T) {
	settings := integrations.GetFlakyRetriesSettings()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	reservation := &flakyRetryBudgetReservation{}
	require.True(t, reservation.reserve())
	execOpts := &executionOptions{flakyRetryBudgetReservation: reservation}
	refundFlakyRetryBudgetReservation(execOpts)
	refundFlakyRetryBudgetReservation(execOpts)
	require.Equal(t, int64(1), atomic.LoadInt64(&settings.RemainingTotalRetryCount))

	reservation = &flakyRetryBudgetReservation{}
	require.True(t, reservation.reserve())
	execOpts.flakyRetryBudgetReservation = reservation
	consumeFlakyRetryBudgetReservation(execOpts)
	refundFlakyRetryBudgetReservation(execOpts)
	require.Zero(t, atomic.LoadInt64(&settings.RemainingTotalRetryCount))
}

func TestProcessRetryFlakyRetryBudgetReservationIsSharedWithSubtestMetadata(t *testing.T) {
	settings := integrations.GetFlakyRetriesSettings()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	reservation := &flakyRetryBudgetReservation{}
	parent := &testExecutionMetadata{
		hasAdditionalFeatureWrapper: true,
		isFlakyTestRetriesEnabled:   true,
		flakyRetryBudgetReservation: reservation,
	}
	child := &testExecutionMetadata{
		hasAdditionalFeatureWrapper: true,
		isFlakyTestRetriesEnabled:   true,
		isARetry:                    true,
		remainingRetries:            1,
	}
	propagateTestExecutionMetadataFlags(child, parent)

	require.Same(t, reservation, child.flakyRetryBudgetReservation)
	require.False(t, isFinalExecution(true, false, child, 0))
	require.Zero(t, atomic.LoadInt64(&settings.RemainingTotalRetryCount))

	localT := createNewTest()
	localT.Fail()
	execOpts := &executionOptions{
		flakyRetryBudgetReservation: reservation,
		options: &runTestWithRetryOptions{
			postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool {
				t.Fatal("the shared reservation must admit the retry without a second budget check")
				return false
			},
		},
	}
	require.True(t, reserveRetryBudgetIfNeeded(execOpts, localT, child, 0))
	consumeFlakyRetryBudgetReservation(execOpts)
	require.Zero(t, atomic.LoadInt64(&settings.RemainingTotalRetryCount))
}

func TestProcessRetryFlakyRetryBudgetReservationIsSharedAcrossParallelSubtests(t *testing.T) {
	settings := integrations.GetFlakyRetriesSettings()
	restoreBudget := setProcessRetryBudgetForTesting(1, 1)
	defer restoreBudget()

	reservation := &flakyRetryBudgetReservation{}
	start := make(chan struct{})
	results := make(chan bool, 2)
	for range 2 {
		child := &testExecutionMetadata{
			hasAdditionalFeatureWrapper: true,
			isFlakyTestRetriesEnabled:   true,
			isARetry:                    true,
			remainingRetries:            1,
			flakyRetryBudgetReservation: reservation,
		}
		go func() {
			<-start
			results <- isFinalExecution(true, false, child, 0)
		}()
	}
	close(start)

	require.False(t, <-results)
	require.False(t, <-results)
	require.True(t, reservation.reserved())
	require.Zero(t, atomic.LoadInt64(&settings.RemainingTotalRetryCount))
}

func TestRunTestWithRetryProcessAllRetriesFailedTagsFinalAttempt(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	childCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			childCalls.Add(1)
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusFail,
				Failed:         true,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	restoreBudget := setProcessRetryBudgetForTesting(2, 100)
	defer restoreBudget()
	identity := newTestIdentity("module", "suite", "TestProcessRetryAllFail")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		bodyCalls.Add(1)
		t.Fail()
	})
	configureProcessRetryBudgetCallbacksForTesting(options, identity, 2)

	runTestWithRetry(options)

	require.Equal(t, int32(1), bodyCalls.Load())
	require.Equal(t, int32(2), childCalls.Load())
	require.Len(t, recorder.tests, 2)
	require.Equal(t, processRetryStatusFail, recorder.tests[0].status)
	require.Equal(t, processRetryStatusFail, recorder.tests[1].status)
	require.Equal(t, constants.TestStatusFail, recorder.tests[1].tags[constants.TestFinalStatus])
	require.Equal(t, "true", recorder.tests[1].tags[constants.TestHasFailedAllRetries])
	require.Equal(t, "process", recorder.tests[0].tags[constants.TestRetryExecutionMode])
	require.Equal(t, "process", recorder.tests[1].tags[constants.TestRetryExecutionMode])
}

func TestRunTestWithRetryProcessSharedTotalRetryBudget(t *testing.T) {
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	childCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			childCalls.Add(1)
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusFail,
				Failed:         true,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	restoreBudget := setProcessRetryBudgetForTesting(3, 1)
	defer restoreBudget()
	runFailingProcessRetryGroup := func(t *testing.T, testName string) {
		t.Helper()
		identity := newTestIdentity("module", "suite", testName)
		createTestMetadata(t, nil)
		defer deleteTestMetadata(t)
		options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
			t.Fail()
		})
		configureProcessRetryBudgetCallbacksForTesting(options, identity, 3)
		runTestWithRetry(options)
	}

	t.Run("first", func(t *testing.T) {
		runFailingProcessRetryGroup(t, "TestProcessRetrySharedBudgetFirst")
	})
	t.Run("second", func(t *testing.T) {
		runFailingProcessRetryGroup(t, "TestProcessRetrySharedBudgetSecond")
	})

	require.Equal(t, int32(1), childCalls.Load())
	require.Len(t, recorder.tests, 1)
	require.Equal(t, "TestProcessRetrySharedBudgetFirst", recorder.tests[0].name)
	require.Zero(t, atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount))
}

func TestEffectiveProcessRetryStatus(t *testing.T) {
	tests := []struct {
		name        string
		attempt     processRetryAttemptResult
		wantStatus  processRetryStatus
		wantFailed  bool
		wantSkipped bool
		wantKind    string
	}{
		{
			name: "pass",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 0,
			},
			wantStatus: processRetryStatusPass,
		},
		{
			name: "pass json with non zero process exit",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 1,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit",
		},
		{
			name: "pass json with observed signal exit",
			attempt: processRetryAttemptResult{
				Result:             processRetryResult{Status: processRetryStatusPass},
				ExitCode:           -1,
				ExitStatusObserved: true,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit",
		},
		{
			name: "structured fail with observed signal exit",
			attempt: processRetryAttemptResult{
				Result:             processRetryResult{Status: processRetryStatusFail, Failed: true},
				ExitCode:           processRetryExitCodeUnset,
				ExitStatusObserved: true,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit",
		},
		{
			name: "structured panic with observed signal exit",
			attempt: processRetryAttemptResult{
				Result:             processRetryResult{Status: processRetryStatusFail, Failed: true, Panic: true},
				ExitCode:           processRetryExitCodeUnset,
				ExitStatusObserved: true,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit",
		},
		{
			name: "skip json with non zero process exit",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusSkip, Skipped: true},
				ExitCode: 1,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit",
		},
		{
			name: "pass json with non zero process exit and retained exit error",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 1,
				Err:      &exec.ExitError{},
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit",
		},
		{
			name: "structured fail keeps test failure classification",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusFail, Failed: true},
				ExitCode: 1,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "test_fail",
		},
		{
			name: "structured fail keeps classification with retained exit error",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusFail, Failed: true},
				ExitCode: 1,
				Err:      &exec.ExitError{},
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "test_fail",
		},
		{
			name: "structured panic",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusFail, Failed: true, Panic: true},
				ExitCode: 1,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "test_panic",
		},
		{
			name: "missing result",
			attempt: processRetryAttemptResult{
				Err:      errProcessRetryResultMissing,
				ExitCode: 0,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "missing_or_not_run",
		},
		{
			name: "malformed result",
			attempt: processRetryAttemptResult{
				Err:      errProcessRetryResultInvalid,
				ExitCode: 0,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "missing_or_not_run",
		},
		{
			name: "not run result",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusNotRun},
				ExitCode: 0,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "missing_or_not_run",
		},
		{
			name: "unset consumed exit code",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: processRetryExitCodeUnset,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_exit_unset",
		},
		{
			name: "timeout",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 0,
				TimedOut: true,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "timeout",
		},
		{
			name: "timeout takes precedence over containment loss",
			attempt: processRetryAttemptResult{
				Result:          processRetryResult{Status: processRetryStatusPass},
				ExitCode:        0,
				TimedOut:        true,
				ContainmentLost: true,
				Err:             errProcessRetryContainmentLost,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "timeout",
		},
		{
			name: "unreaped takes precedence over timeout",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: processRetryExitCodeUnset,
				TimedOut: true,
				Unreaped: true,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_unreaped",
		},
		{
			name: "containment loss",
			attempt: processRetryAttemptResult{
				Result:          processRetryResult{Status: processRetryStatusPass},
				ExitCode:        0,
				ContainmentLost: true,
				Err:             errProcessRetryContainmentLost,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "containment_lost",
		},
		{
			name: "unreaped error precedence",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 0,
				Err:      errProcessRetryChildUnreaped,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_unreaped",
		},
		{
			name: "cancellation precedence",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 0,
				Err:      context.Canceled,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_canceled",
		},
		{
			name: "deadline cancellation precedence",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 0,
				Err:      context.DeadlineExceeded,
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_canceled",
		},
		{
			name: "generic process error",
			attempt: processRetryAttemptResult{
				Result:   processRetryResult{Status: processRetryStatusPass},
				ExitCode: 0,
				Err:      errors.New("process_error_sentinel"),
			},
			wantStatus: processRetryStatusFail,
			wantFailed: true,
			wantKind:   "process_error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveProcessRetryStatus(tt.attempt, false)
			require.Equal(t, tt.wantStatus, got.Status)
			require.Equal(t, tt.wantFailed, got.Failed)
			require.Equal(t, tt.wantSkipped, got.Skipped)
			require.Equal(t, tt.wantKind, got.FailureKind)
		})
	}
}

func TestProcessRetryPanicOnExit0PassResultMapsToProcessExit(t *testing.T) {
	args, ok, reason := buildProcessRetryArgs([]string{"-test.paniconexit0"}, "TestProcessRetryPanicOnExit0", 1, time.Second)
	require.True(t, ok, reason)
	require.Contains(t, args, "-test.paniconexit0")

	attempt := processRetryAttemptResult{
		Result:   processRetryResult{Status: processRetryStatusPass},
		ExitCode: 1,
	}
	effective := effectiveProcessRetryStatus(attempt, false)
	require.Equal(t, processRetryStatusFail, effective.Status)
	require.True(t, effective.Failed)
	require.Equal(t, "process_exit", effective.FailureKind)
}

func TestWriteProcessRetryResultAtomically(t *testing.T) {
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "result.json")
	cfg := processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    "TestAtomicResult",
		Attempt:     2,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	start := time.Now()
	finish := start.Add(time.Millisecond)
	want := processRetryResult{
		Version:        1,
		TestName:       cfg.TestName,
		Attempt:        cfg.Attempt,
		RetryReason:    cfg.RetryReason,
		Status:         processRetryStatusPass,
		StartUnixNano:  start.UnixNano(),
		FinishUnixNano: finish.UnixNano(),
		DurationNanos:  finish.Sub(start).Nanoseconds(),
	}

	require.NoError(t, writeProcessRetryResultAtomically(resultPath, want))
	requireProcessRetryFileMode(t, resultPath, 0o600)
	leftovers, err := filepath.Glob(filepath.Join(dir, ".process-retry-result-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, leftovers)

	got, timingOK, err := readProcessRetryResult(resultPath, cfg)
	require.NoError(t, err)
	require.True(t, timingOK)
	require.Equal(t, want, got)
}

func TestProcessRetryChildResultFixture(t *testing.T) {
	scenario, _ := env.Lookup(processRetryChildResultScenarioEnv)
	if scenario == "" {
		t.Skip("fixture runs only in subprocess")
	}
	if scenario == processRetryOrdinaryDescendantHelperScenario {
		readyPath, _ := env.Lookup(processRetryOrdinaryDescendantReadyPathEnv)
		require.NotEmpty(t, readyPath)
		require.NoError(t, os.WriteFile(readyPath, []byte(strconv.Itoa(os.Getpid())), 0o600))
		_, _ = fmt.Fprintln(os.Stdout, "ordinary descendant stdout ready")
		_, _ = fmt.Fprintln(os.Stderr, "ordinary descendant stderr ready")
		for {
			time.Sleep(time.Hour)
		}
	}
	resultPath, _ := env.LookupPrivate(constants.CIVisibilityInternalRetryProcessResultPath)
	cfg := processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    t.Name(),
		Attempt:     1,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	wrapped := wrapProcessRetryChildTest(func(t *testing.T) {
		switch scenario {
		case "pass":
		case "fail":
			(*T)(t).Error("fixture failure")
		case "skip":
			(*T)(t).Skip("fixture skip")
		case "public_fail":
			GetTest(t).Fail()
		case "public_fail_now":
			GetTest(t).FailNow()
		case "public_errorf":
			GetTest(t).Errorf("fixture %s", "errorf")
		case "public_fatal":
			GetTest(t).Fatal("fixture fatal")
		case "public_fatalf":
			GetTest(t).Fatalf("fixture %s", "fatalf")
		case "public_skipf":
			GetTest(t).Skipf("fixture %s", "skipf")
		case "public_skip_now":
			GetTest(t).SkipNow()
		case "public_parallel":
			GetTest(t).Parallel()
		case "panic":
			panic("body panic sentinel")
		case "goexit":
			runtime.Goexit()
		case "failed_goexit":
			t.Fail()
			runtime.Goexit()
		case "cleanup_panic":
			t.Cleanup(func() { panic("cleanup panic sentinel") })
		case "cleanup_skip":
			t.Cleanup(func() { t.Skip("cleanup skip") })
		case "cleanup_failnow":
			t.Cleanup(func() { t.FailNow() })
		case "body_and_cleanup_panic":
			t.Cleanup(func() { panic("cleanup panic sentinel") })
			panic("body panic sentinel")
		case "cleanup_once":
			counterPath, _ := env.Lookup(processRetryChildCleanupCounterPathEnv)
			require.NotEmpty(t, counterPath)
			t.Cleanup(func() {
				file, err := os.OpenFile(counterPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				require.NoError(t, err)
				defer file.Close()
				_, err = file.WriteString("x")
				require.NoError(t, err)
			})
		case "parallel_subtest_fail":
			t.Run("child", func(t *testing.T) {
				t.Parallel()
				t.Error("parallel child failure")
			})
		case "parallel_top_level_subtest_fail":
			t.Parallel()
			t.Run("child", func(t *testing.T) {
				t.Parallel()
				t.Error("parallel child failure")
			})
		case "parallel_top_level":
			t.Parallel()
		case "stdin_eof":
			stdin, err := io.ReadAll(os.Stdin)
			require.NoError(t, err)
			require.Empty(t, stdin)
		case "panic_large":
			panic(strings.Repeat("x", processRetryErrorMessageMaxBytes*2) + "panic_large_tail_sentinel")
		case processRetryOrdinaryDescendantScenario:
			readyPath, _ := env.Lookup(processRetryOrdinaryDescendantReadyPathEnv)
			require.NotEmpty(t, readyPath)
			args, ok, reason := buildProcessRetryFixtureArgs(os.Args[1:], "TestProcessRetryChildResultFixture")
			require.True(t, ok, reason)
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(),
				"Bypass=true",
				processRetryChildResultScenarioEnv+"="+processRetryOrdinaryDescendantHelperScenario,
				processRetryOrdinaryDescendantReadyPathEnv+"="+readyPath,
			)
			cmd.Stdin = nil
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			require.NoError(t, cmd.Start())
			require.Eventually(t, func() bool {
				payload, err := os.ReadFile(readyPath)
				return err == nil && strings.TrimSpace(string(payload)) != ""
			}, 10*time.Second, 10*time.Millisecond)
			require.NoError(t, cmd.Process.Release())
		default:
			t.Fatalf("unknown scenario %q", scenario)
		}
	}, cfg, newProcessRetryResultWriter(cfg.ResultPath))
	wrapped(t)
}

func enableProcessRetryChildForTesting(t testing.TB) {
	t.Helper()
	restoreEnv := setEnvForTesting(t,
		constants.CIVisibilityEnabledEnvironmentVariable, "true",
		constants.CIVisibilityInternalRetryProcessChild, "true",
	)
	oldEnabled := atomic.LoadInt32(&ciVisibilityEnabledValue)
	atomic.StoreInt32(&ciVisibilityEnabledValue, -1)
	t.Cleanup(func() {
		atomic.StoreInt32(&ciVisibilityEnabledValue, oldEnabled)
		restoreEnv()
	})
}

func setEnvForTesting(t testing.TB, pairs ...string) func() {
	t.Helper()
	require.Equal(t, 0, len(pairs)%2)
	type previousEnv struct {
		key   string
		value string
		ok    bool
	}
	previous := make([]previousEnv, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, value := pairs[i], pairs[i+1]
		old, ok := lookupEnvForTesting(key)
		previous = append(previous, previousEnv{key: key, value: old, ok: ok})
		require.NoError(t, os.Setenv(key, value))
	}
	return func() {
		for i := len(previous) - 1; i >= 0; i-- {
			if previous[i].ok {
				_ = os.Setenv(previous[i].key, previous[i].value)
			} else {
				_ = os.Unsetenv(previous[i].key)
			}
		}
	}
}

func lookupEnvForTesting(key string) (string, bool) {
	if strings.HasPrefix(key, "DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_") {
		return env.LookupPrivate(key)
	}
	return env.Lookup(key)
}

func functionPointer[T any](fn T) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

func readProcessRetryResultForTesting(t testing.TB, path string) processRetryResult {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	var result processRetryResult
	require.NoError(t, json.NewDecoder(file).Decode(&result))
	return result
}

func writeProcessRetryResultForTesting(t testing.TB, path string, result processRetryResult) {
	t.Helper()
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func envSliceToMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		result[key] = value
	}
	return result
}

func envValuesForKey(env []string, key string, caseInsensitive bool) []string {
	var values []string
	for _, entry := range env {
		entryKey, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if (caseInsensitive && strings.EqualFold(entryKey, key)) || (!caseInsensitive && entryKey == key) {
			values = append(values, value)
		}
	}
	return values
}

func registerProcessRetryArgTestFlags(t testing.TB) {
	t.Helper()
	if flag.Lookup("config") == nil {
		flag.String("config", "", "process retry test config flag")
	}
	if flag.Lookup("custom-bool") == nil {
		flag.Bool("custom-bool", false, "process retry test bool flag")
	}
}

func useIsolatedProcessRetryFlagSet(t testing.TB) *flag.FlagSet {
	t.Helper()
	old := flag.CommandLine
	fs := flag.NewFlagSet(t.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flag.CommandLine = fs
	t.Cleanup(func() {
		flag.CommandLine = old
	})
	return fs
}

func manualTempDirForTesting(t testing.TB) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "process-retry-test-*")
	require.NoError(t, err)
	return dir, func() {
		require.NoError(t, os.RemoveAll(dir))
	}
}

const processRetryChildResultScenarioEnv = "PROCESS_RETRY_CHILD_RESULT_SCENARIO"
const processRetryChildCleanupCounterPathEnv = "PROCESS_RETRY_CHILD_CLEANUP_COUNTER_PATH"
const processRetryOrdinaryDescendantReadyPathEnv = "PROCESS_RETRY_ORDINARY_DESCENDANT_READY_PATH"
const processRetryOrdinaryDescendantScenario = "ordinary_descendant"
const processRetryOrdinaryDescendantHelperScenario = "ordinary_descendant_helper"

func buildProcessRetryFixtureArgs(originalArgs []string, testName string) ([]string, bool, string) {
	snapshot := captureProcessRetryArgsSnapshot(originalArgs)
	snapshot.runSelector = ""
	snapshot.skipSelector = ""
	return buildProcessRetryArgsFromSnapshot(snapshot, testName, 1, processRetryDefaultTimeout)
}

func runProcessRetryChildResultFixture(t testing.TB, scenario string) (processRetryResult, int, string) {
	return runProcessRetryChildResultFixtureWithEnv(t, scenario, nil)
}

func runProcessRetryChildResultFixtureWithEnv(t testing.TB, scenario string, extraEnv []string) (processRetryResult, int, string) {
	t.Helper()
	resultPath := filepath.Join(t.TempDir(), "result.json")
	args, ok, reason := buildProcessRetryFixtureArgs(os.Args[1:], "TestProcessRetryChildResultFixture")
	require.True(t, ok, reason)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		"Bypass=true",
		processRetryChildResultScenarioEnv+"="+scenario,
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+resultPath,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		require.Truef(t, ok, "unexpected subprocess error: %v\n%s", err, output)
		exitCode = exitErr.ExitCode()
	}
	return readProcessRetryResultForTesting(t, resultPath), exitCode, string(output)
}

func closeProcessRetryCommandWriters(cmd *exec.Cmd) {
	if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
		_ = stdout.Close()
	}
	if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
		_ = stderr.Close()
	}
}

type processRetryStaticTimer struct {
	ch <-chan time.Time
}

type processRetryRecordingLogger struct {
	mu       locking.Mutex
	messages []string
}

func (l *processRetryRecordingLogger) Log(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, message)
}

func (l *processRetryRecordingLogger) Messages() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.messages, "\n")
}

func (t *processRetryStaticTimer) C() <-chan time.Time { return t.ch }
func (t *processRetryStaticTimer) Stop() bool          { return true }

type processRetryBlockingDoneContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type processRetryObservedDoneContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

type processRetryNthDoneContext struct {
	context.Context
	entered chan struct{}
	target  int32
	calls   atomic.Int32
	once    sync.Once
}

func (c *processRetryObservedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func (c *processRetryNthDoneContext) Done() <-chan struct{} {
	if c.calls.Add(1) >= c.target {
		c.once.Do(func() { close(c.entered) })
	}
	return c.Context.Done()
}

func (c *processRetryBlockingDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return nil
}

func processRetrySuccessfulAttemptHooks(t testing.TB, killTree func(*exec.Cmd) error) processRetryRunnerHooks {
	t.Helper()
	return processRetryRunnerHooks{
		executable:       func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) { return ".", nil },
		args:             func() []string { return nil },
		environ:          os.Environ,
		command:          exec.Command,
		prepareTree:      func(*exec.Cmd) error { return nil },
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			closeProcessRetryCommandWriters(cmd)
			waitCh := make(chan error, 1)
			waitCh <- nil
			return waitCh, nil
		},
		attachTree:    func(*exec.Cmd) error { return nil },
		resumeTree:    func(*exec.Cmd) error { return nil },
		terminateTree: func(*exec.Cmd) error { return nil },
		killTree:      killTree,
		killDirect:    func(*exec.Cmd) error { return nil },
		releaseTree:   func(*exec.Cmd) error { return nil },
		now:           time.Now,
		after:         time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
}

func processRetryChildConfigFromCommandEnv(t testing.TB, commandEnv []string) processRetryChildConfig {
	t.Helper()
	cfg, err := parseProcessRetryChildConfigFromCommandEnv(commandEnv)
	require.NoError(t, err)
	return cfg
}

func parseProcessRetryChildConfigFromCommandEnv(commandEnv []string) (processRetryChildConfig, error) {
	envMap := envSliceToMap(commandEnv)
	attempt, err := strconv.Atoi(envMap[constants.CIVisibilityInternalRetryProcessAttempt])
	if err != nil {
		return processRetryChildConfig{}, fmt.Errorf("parse process retry attempt: %w", err)
	}
	cfg := processRetryChildConfig{
		ResultPath:  envMap[constants.CIVisibilityInternalRetryProcessResultPath],
		TestName:    envMap[constants.CIVisibilityInternalRetryProcessTestName],
		Attempt:     attempt,
		RetryReason: envMap[constants.CIVisibilityInternalRetryProcessReason],
	}
	if cfg.ResultPath == "" || cfg.TestName == "" || cfg.Attempt < 1 || cfg.RetryReason == "" {
		return processRetryChildConfig{}, errors.New("incomplete process retry child command environment")
	}
	return cfg, nil
}

func processRetryRunOptionsForTesting(t *testing.T, identity *testIdentity, target func(*testing.T)) *runTestWithRetryOptions {
	t.Helper()
	require.True(t, registerProcessRetryShutdownAction())
	info := &commonInfo{
		moduleName: identity.ModuleName,
		suiteName:  identity.SuiteName,
		testName:   identity.FullName,
		identity:   identity,
	}
	adjust := func(execMeta *testExecutionMetadata, _ int) {
		execMeta.identity = identity
		execMeta.isFlakyTestRetriesEnabled = true
	}
	return &runTestWithRetryOptions{
		targetFunc:           target,
		t:                    t,
		testInfo:             info,
		processRetryAllowed:  true,
		processRetryIdentity: identity,
		coverageActive:       func() bool { return false },
		fuzzActive:           func() bool { return false },
		preExecMetaAdjust:    adjust,
		preProcessRetryMetaAdjust: func(execMeta *testExecutionMetadata, index int) {
			adjust(execMeta, index)
		},
		preIsLastRetry: func(_ *testExecutionMetadata, _ int, remainingRetries int64) bool {
			return remainingRetries <= 0
		},
		postAdjustRetryCount: func(*testExecutionMetadata, time.Duration) int64 {
			return 1
		},
		postShouldRetry: func(ptrToLocalT *testing.T, _ *testExecutionMetadata, _ int, remainingRetries int64) bool {
			return ptrToLocalT.Failed() && remainingRetries >= 0
		},
	}
}

func setProcessRetryBudgetForTesting(retryCount, remaining int64) func() {
	settings := integrations.GetFlakyRetriesSettings()
	oldRetryCount := settings.RetryCount
	oldTotal := atomic.LoadInt64(&settings.TotalRetryCount)
	oldRemaining := atomic.LoadInt64(&settings.RemainingTotalRetryCount)
	settings.RetryCount = retryCount
	atomic.StoreInt64(&settings.TotalRetryCount, remaining)
	atomic.StoreInt64(&settings.RemainingTotalRetryCount, remaining)
	return func() {
		settings.RetryCount = oldRetryCount
		atomic.StoreInt64(&settings.TotalRetryCount, oldTotal)
		atomic.StoreInt64(&settings.RemainingTotalRetryCount, oldRemaining)
	}
}

func configureProcessRetryBudgetCallbacksForTesting(options *runTestWithRetryOptions, identity *testIdentity, retryCount int64) {
	var allAttemptsPassed int32 = 1
	var allRetriesFailed int32 = 1
	var anyExecutionPassed atomic.Int32
	var anyExecutionFailed atomic.Int32
	adjust := func(execMeta *testExecutionMetadata, _ int) {
		execMeta.identity = identity
		execMeta.isFlakyTestRetriesEnabled = true
		execMeta.allAttemptsPassed = atomic.LoadInt32(&allAttemptsPassed) == 1
		execMeta.allRetriesFailed = atomic.LoadInt32(&allRetriesFailed) == 1
		execMeta.anyExecutionPassed = anyExecutionPassed.Load() == 1
		execMeta.anyExecutionFailed = anyExecutionFailed.Load() == 1
	}
	options.preExecMetaAdjust = adjust
	options.preProcessRetryMetaAdjust = adjust
	options.preIsLastRetry = func(execMeta *testExecutionMetadata, _ int, remainingRetries int64) bool {
		if execMeta.isFlakyTestRetriesEnabled {
			return remainingRetries == 1 || atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount) == 0
		}
		return false
	}
	options.postAdjustRetryCount = func(*testExecutionMetadata, time.Duration) int64 {
		return retryCount
	}
	options.postPerExecution = func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, _ time.Duration) {
		failed := ptrToLocalT.Failed()
		skipped := ptrToLocalT.Skipped()
		if failed || skipped {
			atomic.StoreInt32(&allAttemptsPassed, 0)
		}
		if !failed {
			atomic.StoreInt32(&allRetriesFailed, 0)
		}
		if !failed && !skipped {
			anyExecutionPassed.Store(1)
		}
		if failed {
			anyExecutionFailed.Store(1)
		}
	}
	options.postShouldRetry = func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, _ int, remainingRetries int64) bool {
		return willRetryAfterExecution(
			ptrToLocalT.Failed(),
			ptrToLocalT.Skipped(),
			execMeta,
			remainingRetries,
			atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount),
		)
	}
}

func recordInProcessRetryTagsForParity(t *testing.T) *processRetryRecordingTest {
	t.Helper()
	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()

	identity := newTestIdentity("module", "suite", "TestProcessRetryParity")
	restoreBudget := setProcessRetryBudgetForTesting(1, 100)
	defer restoreBudget()
	module := session.GetOrCreateModule(identity.ModuleName)
	suite := module.GetOrCreateSuite(identity.SuiteName)
	test := suite.CreateTest(identity.FullName)
	execMeta := &testExecutionMetadata{
		identity:                  identity,
		isARetry:                  true,
		isFlakyTestRetriesEnabled: true,
		isANewTest:                true,
	}
	require.False(t, setTestTagsFromExecutionMetadataNoClose(test, execMeta))
	test.SetTag(constants.TestFinalStatus, constants.TestStatusPass)
	test.Close(integrations.ResultStatusPass)

	require.Len(t, recorder.tests, 1)
	return recorder.tests[0]
}

func runProcessRetryParityCase(t *testing.T) *processRetryRecordingTest {
	t.Helper()
	restoreEnv := setEnvForTesting(t, constants.CIVisibilityRetryExecutionModeEnvironmentVariable, "process")
	defer restoreEnv()
	restoreBudget := setProcessRetryBudgetForTesting(1, 100)
	defer restoreBudget()
	oldLimiter := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	defer globalProcessRetryLimiter.Store(oldLimiter)
	supportHooks := processRetrySupportHooks{
		childCleanupSupported: func() bool { return true },
	}
	oldSupport := processRetrySupportHooksOverride.Swap(&supportHooks)
	defer processRetrySupportHooksOverride.Store(oldSupport)

	recorder, restoreSession := setProcessRetryRecordingSessionForTesting(t)
	defer restoreSession()
	bodyCalls := atomic.Int32{}
	childCalls := atomic.Int32{}
	runnerHooks := processRetryRunnerHooks{
		executable: func() (string, error) { return os.Args[0], nil },
		workingDirectory: func() (string, error) {
			return ".", nil
		},
		args:    func() []string { return nil },
		environ: os.Environ,
		command: exec.Command,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			childCalls.Add(1)
			cfg := processRetryChildConfigFromCommandEnv(t, cmd.Env)
			now := time.Now()
			writeProcessRetryResultForTesting(t, cfg.ResultPath, processRetryResult{
				Version:        1,
				TestName:       cfg.TestName,
				Attempt:        cfg.Attempt,
				RetryReason:    cfg.RetryReason,
				Status:         processRetryStatusPass,
				StartUnixNano:  now.UnixNano(),
				FinishUnixNano: now.Add(time.Millisecond).UnixNano(),
				DurationNanos:  int64(time.Millisecond),
			})
			if stdout, ok := cmd.Stdout.(io.WriteCloser); ok {
				_ = stdout.Close()
			}
			if stderr, ok := cmd.Stderr.(io.WriteCloser); ok {
				_ = stderr.Close()
			}
			ch := make(chan error, 1)
			ch <- nil
			return ch, nil
		},
		after: time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
	}
	oldRunner := processRetryRunnerHooksOverride.Swap(&runnerHooks)
	defer processRetryRunnerHooksOverride.Store(oldRunner)

	identity := newTestIdentity("module", "suite", "TestProcessRetryParity")
	createTestMetadata(t, nil)
	defer deleteTestMetadata(t)
	options := processRetryRunOptionsForTesting(t, identity, func(t *testing.T) {
		if bodyCalls.Add(1) == 1 {
			t.Fail()
		}
	})
	options.preExecMetaAdjust = func(execMeta *testExecutionMetadata, index int) {
		execMeta.identity = identity
		execMeta.isFlakyTestRetriesEnabled = true
		execMeta.isANewTest = true
		options.preProcessRetryMetaAdjust(execMeta, index)
	}
	options.preProcessRetryMetaAdjust = func(execMeta *testExecutionMetadata, _ int) {
		execMeta.identity = identity
		execMeta.isFlakyTestRetriesEnabled = true
		execMeta.isANewTest = true
	}

	runTestWithRetry(options)

	require.Equal(t, int32(1), childCalls.Load())
	require.Len(t, recorder.tests, 1)
	return recorder.tests[0]
}

func setProcessRetryRecordingSessionForTesting(t testing.TB) (*processRetryRecordingSession, func()) {
	t.Helper()
	recorder := &processRetryRecordingSession{modules: map[string]*processRetryRecordingModule{}}
	oldSession := session
	oldModulesCounters := modulesCounters
	oldSuitesCounters := suitesCounters
	session = recorder
	modulesCounters = map[string]int{}
	suitesCounters = map[string]int{}
	return recorder, func() {
		session = oldSession
		modulesCounters = oldModulesCounters
		suitesCounters = oldSuitesCounters
	}
}

type processRetryRecordingEvent struct {
	tags         map[string]any
	errorType    string
	errorMessage string
	errorStack   string
}

func (e *processRetryRecordingEvent) Context() context.Context { return context.Background() }
func (e *processRetryRecordingEvent) StartTime() time.Time     { return time.Time{} }
func (e *processRetryRecordingEvent) SetError(options ...integrations.ErrorOption) {
	e.SetTag("error", true)
	for _, option := range options {
		e.errorType = processRetryOptionStringField(option, "errType")
		e.errorMessage = processRetryOptionStringField(option, "message")
		e.errorStack = processRetryOptionStringField(option, "callstack")
	}
}
func (e *processRetryRecordingEvent) SetTag(key string, value any) {
	if e.tags == nil {
		e.tags = map[string]any{}
	}
	e.tags[key] = value
}
func (e *processRetryRecordingEvent) GetTag(key string) (any, bool) {
	value, ok := e.tags[key]
	return value, ok
}

func requireProcessRetryTagsExclude(t testing.TB, tags map[string]any, forbidden ...string) {
	t.Helper()
	for key, value := range tags {
		valueString := fmt.Sprint(value)
		for _, sentinel := range forbidden {
			require.NotContains(t, valueString, sentinel, "tag %q contains forbidden sentinel", key)
		}
	}
}

func requireProcessRetryFileMode(t testing.TB, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm())
}

var _ integrations.TestSession = (*processRetryRecordingSession)(nil)

type processRetryRecordingSession struct {
	processRetryRecordingEvent
	modules    map[string]*processRetryRecordingModule
	tests      []*processRetryRecordingTest
	closeCount int
}

func (s *processRetryRecordingSession) SessionID() uint64        { return 1 }
func (s *processRetryRecordingSession) Command() string          { return "go test" }
func (s *processRetryRecordingSession) Framework() string        { return "go" }
func (s *processRetryRecordingSession) WorkingDirectory() string { return "." }
func (s *processRetryRecordingSession) Close(int, ...integrations.TestSessionCloseOption) {
	s.closeCount++
}
func (s *processRetryRecordingSession) GetOrCreateModule(name string, _ ...integrations.TestModuleStartOption) integrations.TestModule {
	if s.modules == nil {
		s.modules = map[string]*processRetryRecordingModule{}
	}
	if module := s.modules[name]; module != nil {
		return module
	}
	module := &processRetryRecordingModule{session: s, name: name, suites: map[string]*processRetryRecordingSuite{}}
	s.modules[name] = module
	return module
}

var _ integrations.TestModule = (*processRetryRecordingModule)(nil)

type processRetryRecordingModule struct {
	processRetryRecordingEvent
	session    *processRetryRecordingSession
	name       string
	suites     map[string]*processRetryRecordingSuite
	closeCount int
}

func (m *processRetryRecordingModule) ModuleID() uint64                  { return 2 }
func (m *processRetryRecordingModule) Session() integrations.TestSession { return m.session }
func (m *processRetryRecordingModule) Framework() string                 { return "go" }
func (m *processRetryRecordingModule) Name() string                      { return m.name }
func (m *processRetryRecordingModule) Close(...integrations.TestModuleCloseOption) {
	m.closeCount++
}
func (m *processRetryRecordingModule) GetOrCreateSuite(name string, _ ...integrations.TestSuiteStartOption) integrations.TestSuite {
	if m.suites == nil {
		m.suites = map[string]*processRetryRecordingSuite{}
	}
	if suite := m.suites[name]; suite != nil {
		return suite
	}
	suite := &processRetryRecordingSuite{module: m, name: name}
	m.suites[name] = suite
	return suite
}

var _ integrations.TestSuite = (*processRetryRecordingSuite)(nil)

type processRetryRecordingSuite struct {
	processRetryRecordingEvent
	module     *processRetryRecordingModule
	name       string
	closeCount int
}

func (s *processRetryRecordingSuite) SuiteID() uint64                 { return 3 }
func (s *processRetryRecordingSuite) Module() integrations.TestModule { return s.module }
func (s *processRetryRecordingSuite) Name() string                    { return s.name }
func (s *processRetryRecordingSuite) Close(...integrations.TestSuiteCloseOption) {
	s.closeCount++
}
func (s *processRetryRecordingSuite) CreateTest(name string, _ ...integrations.TestStartOption) integrations.Test {
	test := &processRetryRecordingTest{suite: s, name: name}
	s.module.session.tests = append(s.module.session.tests, test)
	return test
}

var _ integrations.Test = (*processRetryRecordingTest)(nil)

type processRetryRecordingTest struct {
	processRetryRecordingEvent
	suite      *processRetryRecordingSuite
	name       string
	status     processRetryStatus
	logs       []string
	skipReason string
	closeCount int
}

func (t *processRetryRecordingTest) TestID() uint64                          { return 4 }
func (t *processRetryRecordingTest) Name() string                            { return t.name }
func (t *processRetryRecordingTest) Suite() integrations.TestSuite           { return t.suite }
func (t *processRetryRecordingTest) SetTestFunc(*runtime.Func)               {}
func (t *processRetryRecordingTest) SetBenchmarkData(string, map[string]any) {}
func (t *processRetryRecordingTest) Log(message, _ string) {
	t.logs = append(t.logs, message)
}
func (t *processRetryRecordingTest) Close(status integrations.TestResultStatus, options ...integrations.TestCloseOption) {
	t.closeCount++
	for _, option := range options {
		if skipReason := processRetryOptionStringField(option, "skipReason"); skipReason != "" {
			t.skipReason = skipReason
		}
	}
	switch status {
	case integrations.ResultStatusPass:
		t.status = processRetryStatusPass
	case integrations.ResultStatusSkip:
		t.status = processRetryStatusSkip
	default:
		t.status = processRetryStatusFail
	}
}

func processRetryOptionStringField(option any, fieldName string) string {
	fn := reflect.ValueOf(option)
	if !fn.IsValid() || fn.Kind() != reflect.Func || fn.Type().NumIn() != 1 || fn.Type().In(0).Kind() != reflect.Pointer {
		return ""
	}
	argument := reflect.New(fn.Type().In(0).Elem())
	fn.Call([]reflect.Value{argument})
	field := argument.Elem().FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

type processRetrySpyContextKey struct{}

var _ integrations.Test = (*processRetrySpyTest)(nil)

type processRetrySpyTest struct {
	name          string
	ctx           context.Context
	setErrorCalls atomic.Int32
	setTagCalls   atomic.Int32
	closeCalls    atomic.Int32
}

func (t *processRetrySpyTest) Context() context.Context {
	if t.ctx != nil {
		return t.ctx
	}
	return context.Background()
}

func (t *processRetrySpyTest) StartTime() time.Time { return time.Time{} }

func (t *processRetrySpyTest) SetError(...integrations.ErrorOption) {
	t.setErrorCalls.Add(1)
}

func (t *processRetrySpyTest) SetTag(string, any) {
	t.setTagCalls.Add(1)
}

func (t *processRetrySpyTest) GetTag(string) (any, bool) { return nil, false }
func (t *processRetrySpyTest) TestID() uint64            { return 0 }
func (t *processRetrySpyTest) Name() string              { return t.name }

func (t *processRetrySpyTest) Suite() integrations.TestSuite {
	return &processRetrySpySuite{}
}

func (t *processRetrySpyTest) Close(integrations.TestResultStatus, ...integrations.TestCloseOption) {
	t.closeCalls.Add(1)
}

func (t *processRetrySpyTest) SetTestFunc(*runtime.Func)               {}
func (t *processRetrySpyTest) SetBenchmarkData(string, map[string]any) {}
func (t *processRetrySpyTest) Log(string, string)                      {}

var _ integrations.TestSuite = (*processRetrySpySuite)(nil)

type processRetrySpySuite struct{}

func (s *processRetrySpySuite) Context() context.Context                   { return context.Background() }
func (s *processRetrySpySuite) StartTime() time.Time                       { return time.Time{} }
func (s *processRetrySpySuite) SetError(...integrations.ErrorOption)       {}
func (s *processRetrySpySuite) SetTag(string, any)                         {}
func (s *processRetrySpySuite) GetTag(string) (any, bool)                  { return nil, false }
func (s *processRetrySpySuite) SuiteID() uint64                            { return 0 }
func (s *processRetrySpySuite) Module() integrations.TestModule            { return &processRetrySpyModule{} }
func (s *processRetrySpySuite) Name() string                               { return "" }
func (s *processRetrySpySuite) Close(...integrations.TestSuiteCloseOption) {}

func (s *processRetrySpySuite) CreateTest(name string, _ ...integrations.TestStartOption) integrations.Test {
	return &processRetrySpyTest{name: name}
}

var _ integrations.TestModule = (*processRetrySpyModule)(nil)

type processRetrySpyModule struct{}

func (m *processRetrySpyModule) Context() context.Context                    { return context.Background() }
func (m *processRetrySpyModule) StartTime() time.Time                        { return time.Time{} }
func (m *processRetrySpyModule) SetError(...integrations.ErrorOption)        {}
func (m *processRetrySpyModule) SetTag(string, any)                          {}
func (m *processRetrySpyModule) GetTag(string) (any, bool)                   { return nil, false }
func (m *processRetrySpyModule) ModuleID() uint64                            { return 0 }
func (m *processRetrySpyModule) Session() integrations.TestSession           { return &processRetrySpySession{} }
func (m *processRetrySpyModule) Framework() string                           { return "" }
func (m *processRetrySpyModule) Name() string                                { return "" }
func (m *processRetrySpyModule) Close(...integrations.TestModuleCloseOption) {}

func (m *processRetrySpyModule) GetOrCreateSuite(name string, _ ...integrations.TestSuiteStartOption) integrations.TestSuite {
	return &processRetrySpySuite{}
}

var _ integrations.TestSession = (*processRetrySpySession)(nil)

type processRetrySpySession struct{}

func (s *processRetrySpySession) Context() context.Context                          { return context.Background() }
func (s *processRetrySpySession) StartTime() time.Time                              { return time.Time{} }
func (s *processRetrySpySession) SetError(...integrations.ErrorOption)              {}
func (s *processRetrySpySession) SetTag(string, any)                                {}
func (s *processRetrySpySession) GetTag(string) (any, bool)                         { return nil, false }
func (s *processRetrySpySession) SessionID() uint64                                 { return 0 }
func (s *processRetrySpySession) Command() string                                   { return "" }
func (s *processRetrySpySession) Framework() string                                 { return "" }
func (s *processRetrySpySession) WorkingDirectory() string                          { return "" }
func (s *processRetrySpySession) Close(int, ...integrations.TestSessionCloseOption) {}

func (s *processRetrySpySession) GetOrCreateModule(name string, _ ...integrations.TestModuleStartOption) integrations.TestModule {
	return &processRetrySpyModule{}
}
