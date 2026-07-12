// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/envconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type (
	// instrumentationMetadata contains the internal instrumentation metadata
	instrumentationMetadata struct {
		IsInternal bool
	}

	// testExecutionMetadata contains metadata regarding an unique *testing.T or *testing.B execution
	testExecutionMetadata struct {
		test                         integrations.Test     // internal CI Visibility test event
		originalTest                 *testing.T            // original test that was executed
		parallelForwardState         *parallelForwardState // shared state used to forward t.Parallel from retry clones to the original test
		parallelForwarded            atomic.Bool           // tracks whether this execution already forwarded t.Parallel to the original test
		error                        atomic.Int32          // flag to check if the test event has error data already
		skipped                      atomic.Int32          // flag to check if the test event has skipped data already
		panicData                    any                   // panic data recovered from an internal test execution when using an additional feature wrapper
		panicStacktrace              string                // stacktrace from the panic recovered from an internal test
		skipReason                   string                // skip reason captured from instrumentCloseAndSkip when hasAdditionalFeatureWrapper is true
		processRetryError            atomic.Pointer[processRetryErrorInfo]
		processRetrySkipReason       atomic.Pointer[string]
		processRetryPanic            atomic.Pointer[processRetryErrorInfo]
		processRetryOwner            *testExecutionMetadata
		isARetry                     bool // flag to tag if a current test execution is a retry
		isANewTest                   bool // flag to tag if a current test a new test
		isAModifiedTest              bool // flag to tag if a current test a modified test
		isEarlyFlakeDetectionEnabled bool // flag to tag if Early Flake Detection is enabled for this execution
		isFlakyTestRetriesEnabled    bool // flag to tag if Flaky Test Retries is enabled for this execution
		isItrForcedRun               bool // flag to preserve ITR forced-run state across parent-owned process retries
		flakyRetryBudgetReservation  *flakyRetryBudgetReservation
		isQuarantined                bool          // flag to check if the test is quarantined
		isDisabled                   bool          // flag to check if the test is disabled
		isAttemptToFix               bool          // flag to check if the test is marked as attempt to fix
		isLastRetry                  bool          // flag to check if the current execution is the last retry
		allAttemptsPassed            bool          // flag to check if all attempts passed for a test marked as attempt to fix
		allRetriesFailed             bool          // flag to check if all retries failed for a test
		hasAdditionalFeatureWrapper  bool          // flag to check if the current execution is part of an additional feature wrapper
		identity                     *testIdentity // identity of the current execution (test or subtest)
		hasExplicitQuarantined       bool          // flag to mark if quarantine state comes from explicit configuration
		hasExplicitDisabled          bool          // flag to mark if disabled state comes from explicit configuration
		hasExplicitAttemptToFix      bool          // flag to mark if attempt-to-fix state comes from explicit configuration
		suppressParentRetryMetadata  bool          // prevents metadata-only subtest overrides from inheriting parent retry-wrapper control fields

		// Fields for test.final_status computation
		anyExecutionPassed            bool               // tracks if any prior execution passed (for final status calculation)
		anyExecutionFailed            bool               // tracks if any prior execution failed (for final status calculation)
		remainingRetries              int64              // remaining retries at the start of this execution
		shouldOrchestrateAttemptToFix bool               // whether this wrapper controls ATF retries
		isEfdInParallel               bool               // true only when parallel EFD path is active
		cleanupResult                 *testCleanupResult // records cleanup completion for this retry attempt.
	}

	// runTestWithRetryOptions contains the options for calling runTestWithRetry function
	runTestWithRetryOptions struct {
		targetFunc                    func(t *testing.T) // target function to retry
		t                             *testing.T         // test to be executed
		parallelEFDAllowed            bool               // allows the internal parallel EFD scheduler when the effective execution qualifies
		testInfo                      *commonInfo
		processRetryAllowed           bool
		processRetryMode              retryExecutionMode
		processRetryModeSet           bool
		processRetryIdentity          *testIdentity
		coverageActive                func() bool
		fuzzActive                    func() bool
		processRetryContext           func() context.Context
		processRetryGuardsSnapshotted bool
		processRetryCoverageGuardSet  bool
		processRetryCoverageActive    bool
		processRetryFuzzGuardSet      bool
		processRetryFuzzActive        bool

		// function to modify the execution metadata before each execution (first callback executed). It's also called before postOnRetryEnd to do a final sync
		preExecMetaAdjust func(execMeta *testExecutionMetadata, executionIndex int)

		// function to modify execution metadata for parent-owned process retry attempts.
		preProcessRetryMetaAdjust func(execMeta *testExecutionMetadata, executionIndex int)

		// function to decide whether we are in the last retry (second callback executed if we are in a retry execution)
		preIsLastRetry func(execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool

		// adjust retry count function depending on the duration of the first execution (first callback executed post test execution only in the first execution of the test)
		postAdjustRetryCount func(execMeta *testExecutionMetadata, duration time.Duration) int64

		// function to run after each test execution (second callback executed after test execution)
		postPerExecution func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, duration time.Duration)

		// function to decide whether we want to perform a retry (third callback executed after test execution)
		postShouldRetry func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, remainingRetries int64) bool

		// function executed when all execution have finished (last callback executed after all test executions(+retries))
		postOnRetryEnd func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T)
	}

	additionalFeatureWrapperOptions struct {
		processRetryAllowed bool
		coverageActive      func() bool
		fuzzActive          func() bool
	}

	// executionOptions holds the execution options for the test
	executionOptions struct {
		mutex                        sync.Locker              // mutex for synchronizing test iterations
		options                      *runTestWithRetryOptions // options for the test execution
		parallelForwardState         *parallelForwardState    // shared t.Parallel forwarding state for all attempts in this retry group
		executionIndex               int                      // current execution index
		retryCount                   int64                    // remaining retry count
		originalExecutionMetadata    *testExecutionMetadata   // original test execution metadata
		panicExecutionMetadata       *testExecutionMetadata   // panicked execution metadata
		ptrToLocalT                  *testing.T               // pointer to the local test instance
		executionMetadata            *testExecutionMetadata   // current test execution metadata
		module                       integrations.TestModule  // module associated with the test
		suite                        integrations.TestSuite   // suite associated with the test
		effectiveParallelEFDActive   bool                     // true only after runTestWithRetry selects the bounded parallel EFD branch
		processRetryConsumedAttempt  bool                     // true after this retry group emits a process retry attempt.
		processRetryMetadataSnapshot *processRetryMetadataSnapshot
		processRetryLaunchBaseline   *processRetryLaunchBaseline
		processRetryShutdown         <-chan struct{}
		flakyRetryBudgetReservation  *flakyRetryBudgetReservation
	}

	flakyRetryBudgetReservation struct {
		state atomic.Int32
	}

	// testCleanupResult captures how testing cleanup execution completed for a retry attempt.
	testCleanupResult struct {
		panicData       any    // panic value returned by testing.common.runCleanup.
		panicStacktrace string // stacktrace captured when cleanup returned a panic value.
		goexit          bool   // true when cleanup called runtime.Goexit before runCleanup returned.
		ran             bool   // true after this attempt has executed its testing cleanups.
	}

	// additionalFeatureMetadata is the effective per-test state used to select and apply CI Visibility additional features.
	additionalFeatureMetadata struct {
		identity                      *testIdentity           // fully-qualified test or subtest identity
		isTestManagementEnabled       bool                    // whether Test Management is enabled for the session
		isEarlyFlakeDetectionEnabled  bool                    // whether EFD remains effective for this test after suppression
		isFlakyTestRetriesEnabled     bool                    // whether FTR remains effective for this test after suppression
		isQuarantined                 bool                    // effective Test Management quarantine directive
		isDisabled                    bool                    // effective Test Management disabled directive
		isAttemptToFix                bool                    // effective Test Management attempt-to-fix directive
		isNew                         bool                    // selector-level known-new test result for EFD
		isModified                    bool                    // selector-level modified-test result when known ahead of span creation
		hasExplicitQuarantined        bool                    // true when quarantine was set by an exact Test Management match
		hasExplicitDisabled           bool                    // true when disabled was set by an exact Test Management match
		hasExplicitAttemptToFix       bool                    // true when attempt-to-fix was set by an exact Test Management match
		managementMatchKind           testManagementMatchKind // specificity of the Test Management match
		shouldOrchestrateAttemptToFix bool                    // true when this layer owns the ATF retry lifecycle
	}

	// additionalFeaturePath identifies how CI Visibility should apply additional feature behavior for a test.
	additionalFeaturePath int

	// additionalFeatureSelection records the selected path and the effective reasons used to choose it.
	additionalFeatureSelection struct {
		path    additionalFeaturePath // selected additional-feature execution path
		reasons []string              // debug-friendly reasons derived from the same metadata snapshot as the path
	}

	// parallelForwardState coordinates t.Parallel forwarding for Datadog-managed
	// test clones that all point at the same original *testing.T.
	parallelForwardState struct {
		mu          sync.Mutex // guards the forwarding and forwarded state
		cond        *sync.Cond // wakes waiting retry attempts after an active forward finishes
		forwarding  bool       // true while an attempt is inside the original testing.T.Parallel call
		forwarded   bool       // true after the retry group has successfully forwarded Parallel once
		duplicateMu sync.Mutex // serializes duplicate Parallel calls so the Go runtime produces the standard panic deterministically
	}
)

const unexpectedTestTerminationMessage = "test executed panic(nil) or runtime.Goexit"

var (
	// ciVisibilityEnabledValue holds a value to check if ci visibility is enabled or not (1 = enabled / 0 = disabled)
	ciVisibilityEnabledValue int32 = -1

	// instrumentationMap holds a map of *runtime.Func for tracking instrumented functions
	instrumentationMap = map[*runtime.Func]*instrumentationMetadata{}

	// instrumentationMapMutex is a read-write mutex for synchronizing access to instrumentationMap.
	instrumentationMapMutex sync.RWMutex

	// ciVisibilityTests holds a map of *testing.T or *testing.B to execution metadata for tracking tests.
	ciVisibilityTestMetadata = map[unsafe.Pointer]*testExecutionMetadata{}

	// ciVisibilityTestMetadataMutex is a read-write mutex for synchronizing access to ciVisibilityTestMetadata.
	ciVisibilityTestMetadataMutex sync.RWMutex
)

const (
	// internalParallelEFDMaxConcurrency bounds the experimental parallel-EFD scheduler without adding another configuration key.
	internalParallelEFDMaxConcurrency int64 = 4

	// additionalFeaturePathNone keeps the original instrumentation path without preloading additional feature metadata.
	additionalFeaturePathNone additionalFeaturePath = iota
	// additionalFeaturePathMetadataOnly preloads exact subtest metadata without retry isolation.
	additionalFeaturePathMetadataOnly
	// additionalFeaturePathDisabledFast applies disabled-test metadata and skips without cloning testing.T.
	additionalFeaturePathDisabledFast
	// additionalFeaturePathRetryWrapper uses retry isolation for features that need owned execution control.
	additionalFeaturePathRetryWrapper
)

// String returns a stable label for additional-feature path debug logs.
func (p additionalFeaturePath) String() string {
	switch p {
	case additionalFeaturePathNone:
		return "none"
	case additionalFeaturePathMetadataOnly:
		return "metadata_only"
	case additionalFeaturePathDisabledFast:
		return "disabled_fast_path"
	case additionalFeaturePathRetryWrapper:
		return "retry_wrapper"
	default:
		return fmt.Sprintf("unknown(%d)", p)
	}
}

// newParallelForwardState creates the shared t.Parallel forwarding state for one
// runTestWithRetry invocation. The returned value must be shared by pointer only.
func newParallelForwardState() *parallelForwardState {
	state := &parallelForwardState{}
	state.cond = sync.NewCond(&state.mu)
	return state
}

// forward calls Parallel on the original test at most once for a retry group.
// It deliberately avoids sync.Once because testing.T.Parallel can block or panic,
// and waiters must not proceed until the real Go scheduling barrier has returned.
func (s *parallelForwardState) forward(original *testing.T) {
	s.mu.Lock()
	for s.forwarding {
		s.cond.Wait()
	}
	if s.forwarded {
		s.mu.Unlock()
		return
	}
	s.forwarding = true
	s.mu.Unlock()

	completed := false
	var panicValue any
	defer func() {
		if !completed {
			panicValue = recover()
		}

		s.mu.Lock()
		if completed {
			s.forwarded = true
		}
		s.forwarding = false
		s.cond.Broadcast()
		s.mu.Unlock()

		if panicValue != nil {
			panic(panicValue)
		}
	}()

	original.Parallel()
	completed = true
}

// callDuplicate forwards a second Parallel call from the same execution to the
// original test so the Go runtime preserves its standard duplicate-call panic.
func (s *parallelForwardState) callDuplicate(original *testing.T) {
	s.mu.Lock()
	for s.forwarding {
		s.cond.Wait()
	}
	s.mu.Unlock()

	s.duplicateMu.Lock()
	defer s.duplicateMu.Unlock()

	original.Parallel()
}

// isCiVisibilityEnabled reports whether DD_CIVISIBILITY_ENABLED enables CI Visibility for this process.
func isCiVisibilityEnabled() bool {
	// let's check if the value has already been loaded from the env-vars
	enabledValue := atomic.LoadInt32(&ciVisibilityEnabledValue)
	if enabledValue == -1 {
		// Get the DD_CIVISIBILITY_ENABLED env var, if not present we default to false (for now). This is because if we are here, it means
		// that the process was instrumented for ci visibility or by using orchestrion.
		// So effectively this env-var will act as a kill switch for cases where the code is instrumented, but
		// we don't want the civisibility instrumentation to be enabled.
		// *** For preview releases we will default to false, meaning that the use of ci visibility must be opt-in ***
		mode, ok := envconfig.FromEnv()
		if ok && envconfig.Enabled(mode) {
			atomic.StoreInt32(&ciVisibilityEnabledValue, 1)
			return true
		}
		atomic.StoreInt32(&ciVisibilityEnabledValue, 0)
		return false

	}

	return enabledValue == 1
}

// getInstrumentationMetadata gets the stored instrumentation metadata for a given *runtime.Func.
func getInstrumentationMetadata(fn *runtime.Func) *instrumentationMetadata {
	instrumentationMapMutex.RLock()
	defer instrumentationMapMutex.RUnlock()
	if v, ok := instrumentationMap[fn]; ok {
		return v
	}
	return nil
}

// setInstrumentationMetadata stores an instrumentation metadata for a given *runtime.Func.
func setInstrumentationMetadata(fn *runtime.Func, metadata *instrumentationMetadata) {
	instrumentationMapMutex.Lock()
	defer instrumentationMapMutex.Unlock()
	instrumentationMap[fn] = metadata
}

// createTestMetadata creates the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func createTestMetadata(tb testing.TB, originalTest *testing.T) *testExecutionMetadata {
	ciVisibilityTestMetadataMutex.Lock()
	defer ciVisibilityTestMetadataMutex.Unlock()
	execMetadata := &testExecutionMetadata{originalTest: originalTest}
	ciVisibilityTestMetadata[reflect.ValueOf(tb).UnsafePointer()] = execMetadata
	return execMetadata
}

// getTestMetadata retrieves the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func getTestMetadata(tb testing.TB) *testExecutionMetadata {
	return getTestMetadataFromPointer(reflect.ValueOf(tb).UnsafePointer())
}

// getTestMetadataFromPointer retrieves the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common using a pointer
func getTestMetadataFromPointer(ptr unsafe.Pointer) *testExecutionMetadata {
	ciVisibilityTestMetadataMutex.RLock()
	defer ciVisibilityTestMetadataMutex.RUnlock()
	if v, ok := ciVisibilityTestMetadata[ptr]; ok {
		return v
	}
	return nil
}

// deleteTestMetadata delete the CI visibility test metadata associated with a given *testing.T, *testing.B, *testing.common
func deleteTestMetadata(tb testing.TB) {
	ciVisibilityTestMetadataMutex.Lock()
	defer ciVisibilityTestMetadataMutex.Unlock()
	delete(ciVisibilityTestMetadata, reflect.ValueOf(tb).UnsafePointer())
}

// checkIfCIVisibilityExitIsRequiredByPanic checks the additional features settings to decide if we allow individual tests to panic or not
func checkIfCIVisibilityExitIsRequiredByPanic() bool {
	// Apply additional features
	settings := integrations.GetSettings()

	// If we don't plan to do retries then we allow to panic
	return !settings.FlakyTestRetriesEnabled && !settings.EarlyFlakeDetection.Enabled
}

// selectAdditionalFeaturePath chooses the lightest execution path that still preserves the effective feature behavior.
func selectAdditionalFeaturePath(meta *additionalFeatureMetadata, impactedTestsEnabled bool, flakyRetryCount, remainingFlakyRetryBudget int64, needsMetadataOnly bool) additionalFeatureSelection {
	if meta == nil {
		return additionalFeatureSelection{path: additionalFeaturePathNone}
	}

	if meta.isDisabled && !meta.isAttemptToFix {
		reasons := []string{"test_management_disabled"}
		if meta.isQuarantined {
			reasons = append(reasons, "test_management_quarantined")
		}
		return additionalFeatureSelection{path: additionalFeaturePathDisabledFast, reasons: reasons}
	}

	if meta.isAttemptToFix && meta.shouldOrchestrateAttemptToFix {
		return additionalFeatureSelection{path: additionalFeaturePathRetryWrapper, reasons: []string{"attempt_to_fix"}}
	}

	if meta.isDisabled {
		reasons := []string{"test_management_disabled"}
		if meta.isAttemptToFix {
			reasons = append(reasons, "attempt_to_fix")
		}
		return additionalFeatureSelection{path: additionalFeaturePathRetryWrapper, reasons: reasons}
	}

	if meta.isQuarantined {
		reasons := []string{"test_management_quarantined"}
		if meta.isAttemptToFix {
			reasons = append(reasons, "attempt_to_fix")
		}
		return additionalFeatureSelection{path: additionalFeaturePathRetryWrapper, reasons: reasons}
	}

	if needsMetadataOnly {
		return additionalFeatureSelection{path: additionalFeaturePathMetadataOnly, reasons: []string{"inherited_subtest_state"}}
	}

	reasons := make([]string, 0, 3)
	if meta.isEarlyFlakeDetectionEnabled {
		if meta.isNew {
			reasons = append(reasons, "efd_new_test")
		} else if impactedTestsEnabled {
			reasons = append(reasons, "efd_modified_candidate")
		}
	}
	if meta.isFlakyTestRetriesEnabled && flakyRetryCount > 0 && remainingFlakyRetryBudget > 0 {
		reasons = append(reasons, "flaky_retry")
	}
	if len(reasons) == 0 {
		return additionalFeatureSelection{path: additionalFeaturePathNone}
	}
	return additionalFeatureSelection{path: additionalFeaturePathRetryWrapper, reasons: reasons}
}

// logAdditionalFeatureSelection writes the selected non-default path with the same effective reasons used by the selector.
func logAdditionalFeatureSelection(meta *additionalFeatureMetadata, selection additionalFeatureSelection) {
	if meta == nil || selection.path == additionalFeaturePathNone {
		return
	}
	name := "<unknown>"
	if meta.identity != nil {
		name = meta.identity.FullName
	}
	log.Debug("gotesting: additional feature path test=%s path=%s reasons=[%s]", name, selection.path.String(), strings.Join(selection.reasons, " "))
}

// applyAdditionalFeatureMetadataToExecution copies effective feature metadata into one concrete test execution.
func applyAdditionalFeatureMetadataToExecution(execMeta *testExecutionMetadata, meta *additionalFeatureMetadata) {
	if execMeta == nil || meta == nil {
		return
	}
	execMeta.identity = meta.identity
	if meta.hasExplicitQuarantined {
		// Exact Test Management data is applied before parent propagation, which still OR-inherits quarantine today.
		execMeta.isQuarantined = meta.isQuarantined
		execMeta.hasExplicitQuarantined = true
	} else {
		// Ancestor-level quarantine accumulates with state propagated from parent executions.
		execMeta.isQuarantined = execMeta.isQuarantined || meta.isQuarantined
	}
	if meta.hasExplicitDisabled {
		// Exact Test Management data is applied before parent propagation, which still OR-inherits disabled today.
		execMeta.isDisabled = meta.isDisabled
		execMeta.hasExplicitDisabled = true
	} else {
		// Ancestor-level disabled state accumulates with state propagated from parent executions.
		execMeta.isDisabled = execMeta.isDisabled || meta.isDisabled
	}
	if meta.hasExplicitAttemptToFix {
		// Only explicit attempt-to-fix data can clear inherited attempt-to-fix state.
		execMeta.isAttemptToFix = meta.isAttemptToFix
		execMeta.hasExplicitAttemptToFix = true
	} else {
		// Non-exact attempt-to-fix state is inherited additively.
		execMeta.isAttemptToFix = execMeta.isAttemptToFix || meta.isAttemptToFix
	}
	execMeta.isEarlyFlakeDetectionEnabled = execMeta.isEarlyFlakeDetectionEnabled || meta.isEarlyFlakeDetectionEnabled
	execMeta.isFlakyTestRetriesEnabled = execMeta.isFlakyTestRetriesEnabled || meta.isFlakyTestRetriesEnabled
	execMeta.isANewTest = execMeta.isANewTest || meta.isNew
	execMeta.isAModifiedTest = execMeta.isAModifiedTest || meta.isModified
	execMeta.shouldOrchestrateAttemptToFix = meta.shouldOrchestrateAttemptToFix
}

// syncFeatureMetadataFromExecution keeps wrapper-level metadata aligned with the latest concrete execution.
func syncFeatureMetadataFromExecution(meta *additionalFeatureMetadata, execMeta *testExecutionMetadata) {
	if meta == nil || execMeta == nil {
		return
	}
	meta.identity = execMeta.identity
	// isDisabled and isQuarantined use a one-way ratchet: inherited state from the parent
	// (propagated via propagateTestExecutionMetadataFlags) can only accumulate true, never
	// clear back to false. This serves two goals simultaneously:
	//   1. Prevents corruption: a transient execMeta.isDisabled=false (due to propagation
	//      ordering) cannot zero ptrMeta.isDisabled=true across retry iterations, which would
	//      cause all subsequent spans to lose the disabled tag.
	//   2. Preserves child-inherits-parent skip: when a child has its own retry wrapper but its
	//      parent is disabled/quarantined, propagateTestExecutionMetadataFlags ORs the parent
	//      state into execMeta. The ratchet lets that accumulated true reach ptrMeta so that
	//      postOnRetryEnd (which reads ptrMeta.isDisabled || ptrMeta.isQuarantined) still calls
	//      SkipNow as expected instead of failing the Go test run.
	meta.isDisabled = meta.isDisabled || execMeta.isDisabled
	meta.isQuarantined = meta.isQuarantined || execMeta.isQuarantined
	// isAttemptToFix and hasExplicit* are set once from the authoritative management directive
	// and do not need to be synced from execution metadata — they are stable by construction.
	meta.isEarlyFlakeDetectionEnabled = execMeta.isEarlyFlakeDetectionEnabled
	meta.isFlakyTestRetriesEnabled = execMeta.isFlakyTestRetriesEnabled
	meta.isNew = execMeta.isANewTest
	meta.isModified = execMeta.isAModifiedTest
}

// wrapWithAdditionalFeatureMetadata preloads metadata without entering retry isolation.
func wrapWithAdditionalFeatureMetadata(f func(*testing.T), meta *additionalFeatureMetadata, suppressParentRetryMetadata, skipAfterRun bool) func(*testing.T) {
	wrapper := func(t *testing.T) {
		t.Helper()
		execMeta := getTestMetadata(t)
		createdMetadata := false
		deletedMetadata := false
		if execMeta == nil {
			execMeta = createTestMetadata(t, nil)
			createdMetadata = true
		}
		defer func() {
			if createdMetadata && !deletedMetadata {
				deleteTestMetadata(t)
			}
		}()

		applyAdditionalFeatureMetadataToExecution(execMeta, meta)
		if suppressParentRetryMetadata {
			execMeta.suppressParentRetryMetadata = true
		}

		f(t)

		skipCurrentTest := skipAfterRun || (execMeta.isDisabled && !execMeta.isAttemptToFix)
		if skipCurrentTest {
			// The disabled fast path, and inherited-disabled metadata-only subtests,
			// close the CI Visibility event before this point. Mark skip instrumentation
			// as already handled so Go's SkipNow does not close it again.
			execMeta.skipped.Store(1)
			if createdMetadata {
				deleteTestMetadata(t)
				deletedMetadata = true
			}
			t.SkipNow()
		}
	}
	setInstrumentationMetadata(runtime.FuncForPC(reflect.ValueOf(wrapper).Pointer()), &instrumentationMetadata{IsInternal: true})
	return wrapper
}

// applyAdditionalFeaturesToTestFunc applies all the additional features as wrapper of a func(*testing.T).
// parentExecMeta is optional and allows subtests to inherit behaviour from their parent test when needed.
func applyAdditionalFeaturesToTestFunc(
	f func(*testing.T),
	testInfo *commonInfo,
	parentExecMeta *testExecutionMetadata,
	wrapperOpts additionalFeatureWrapperOptions,
) func(*testing.T) {
	// Apply additional features
	settings := integrations.GetSettings()

	// Ensure that session-level additional features and capability tags are initialized before any path returns early.
	_ = integrations.GetKnownTests()

	// If none of the additional features are enabled, return the original function.
	if !settings.TestManagement.Enabled && !settings.EarlyFlakeDetection.Enabled && !settings.FlakyTestRetriesEnabled {
		return f
	}

	identity := testInfo.identity
	if identity == nil {
		// Derive an identity for tests that did not populate it (such as subtests discovered at runtime).
		identity = newTestIdentity(testInfo.moduleName, testInfo.suiteName, testInfo.testName)
	}
	isSubtest := len(identity.Segments) > 1

	meta := additionalFeatureMetadata{
		identity:                     identity,
		isTestManagementEnabled:      settings.TestManagement.Enabled,
		isEarlyFlakeDetectionEnabled: settings.EarlyFlakeDetection.Enabled,
		isFlakyTestRetriesEnabled:    settings.FlakyTestRetriesEnabled,
		managementMatchKind:          testManagementMatchNone,
	}

	// Test Management feature
	if meta.isTestManagementEnabled {
		// Pull the most specific directives available for the current identity.
		if data, matchKind, ok := getTestManagementData(identity); ok && data != nil {
			meta.managementMatchKind = matchKind
			meta.isQuarantined = data.Quarantined
			meta.isDisabled = data.Disabled
			meta.isAttemptToFix = data.AttemptToFix
			if matchKind == testManagementMatchExact {
				meta.hasExplicitQuarantined = true
				meta.hasExplicitDisabled = true
				meta.hasExplicitAttemptToFix = true
			}
		}
	}

	// determine whether attempt-to-fix retries should be orchestrated at this level
	meta.shouldOrchestrateAttemptToFix = meta.isAttemptToFix
	if parentExecMeta != nil && parentExecMeta.isAttemptToFix {
		// The parent already controls the attempt-to-fix loop; subtests should only orchestrate if explicitly requested.
		meta.shouldOrchestrateAttemptToFix = meta.hasExplicitAttemptToFix && meta.isAttemptToFix && !parentExecMeta.isAttemptToFix
	}

	if isSubtest {
		if !settings.SubtestFeaturesEnabled {
			// Feature gate keeps legacy behaviour when subtests support is disabled.
			return f
		}
		// Require an exact match before applying subtest-specific directives; fallbacks remain parent-scoped.
		if meta.managementMatchKind != testManagementMatchExact {
			return f
		}
		// Subtests currently inherit parent EFD/flaky retry behaviour; disable here to avoid double wrapping.
		meta.isEarlyFlakeDetectionEnabled = false
		meta.isFlakyTestRetriesEnabled = false
	}

	if (meta.isDisabled || meta.isQuarantined) && !meta.isAttemptToFix {
		// Disabled and quarantined tests have Test Management semantics; unrelated retry features must not own them.
		meta.isEarlyFlakeDetectionEnabled = false
		meta.isFlakyTestRetriesEnabled = false
	}

	// Early Flake Detection feature
	if meta.isEarlyFlakeDetectionEnabled {
		// Record whether the test is new so we can surface it in spans later.
		isKnown, hasKnownData := isKnownTest(testInfo)
		meta.isNew = hasKnownData && !isKnown
	}

	var flakyRetryCount int64
	var remainingFlakyRetryBudget int64
	if meta.isFlakyTestRetriesEnabled {
		flakyRetriesSettings := integrations.GetFlakyRetriesSettings()
		flakyRetryCount = flakyRetriesSettings.RetryCount
		remainingFlakyRetryBudget = atomic.LoadInt64(&flakyRetriesSettings.RemainingTotalRetryCount)
	}

	parentAttemptToFixActive := parentExecMeta != nil && parentExecMeta.isAttemptToFix
	needsMetadataOnly := isSubtest &&
		meta.managementMatchKind == testManagementMatchExact &&
		parentAttemptToFixActive &&
		!meta.shouldOrchestrateAttemptToFix &&
		!meta.isDisabled &&
		!meta.isQuarantined
	selection := selectAdditionalFeaturePath(&meta, settings.ImpactedTestsEnabled, flakyRetryCount, remainingFlakyRetryBudget, needsMetadataOnly)
	logAdditionalFeatureSelection(&meta, selection)

	// get the pointer to use the reference in the wrapper
	ptrMeta := &meta

	switch selection.path {
	case additionalFeaturePathNone:
		return f
	case additionalFeaturePathMetadataOnly:
		return wrapWithAdditionalFeatureMetadata(f, ptrMeta, true, false)
	case additionalFeaturePathDisabledFast:
		return wrapWithAdditionalFeatureMetadata(f, ptrMeta, false, true)
	}

	// Create a unified wrapper that will use a single runTestWithRetry call.
	wrapper := func(t *testing.T) {
		t.Helper()
		originalExecMeta := getTestMetadata(t)

		// For Early Flake Detection: counters used to collect test results.
		var testPassCount, testSkipCount, testFailCount int
		// For Test Management and auto retries.
		var allAttemptsPassed int32 = 1
		var allRetriesFailed int32 = 1
		// For test.final_status computation: track pass/fail across all executions.
		var anyExecutionPassed atomic.Int32
		var anyExecutionFailed atomic.Int32

		runTestWithRetry(&runTestWithRetryOptions{
			targetFunc:           f,
			t:                    t,
			parallelEFDAllowed:   internal.BoolEnv(constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled, false),
			testInfo:             testInfo,
			processRetryAllowed:  wrapperOpts.processRetryAllowed,
			processRetryIdentity: identity,
			coverageActive:       wrapperOpts.coverageActive,
			fuzzActive:           wrapperOpts.fuzzActive,
			preExecMetaAdjust: func(execMeta *testExecutionMetadata, _ int) {
				// Synchronize the test execution metadata with the original test execution metadata.

				applyAdditionalFeatureMetadataToExecution(execMeta, ptrMeta)
				execMeta.allAttemptsPassed = atomic.LoadInt32(&allAttemptsPassed) == 1
				execMeta.allRetriesFailed = atomic.LoadInt32(&allRetriesFailed) == 1

				// Copy test.final_status tracking state from wrapper-level atomics.
				execMeta.anyExecutionPassed = anyExecutionPassed.Load() == 1
				execMeta.anyExecutionFailed = anyExecutionFailed.Load() == 1

				// Propagate flags from the original test metadata.
				propagateTestExecutionMetadataFlags(execMeta, originalExecMeta)

				syncFeatureMetadataFromExecution(ptrMeta, execMeta)
			},
			preProcessRetryMetaAdjust: func(execMeta *testExecutionMetadata, _ int) {
				execMeta.allAttemptsPassed = atomic.LoadInt32(&allAttemptsPassed) == 1
				execMeta.allRetriesFailed = atomic.LoadInt32(&allRetriesFailed) == 1
				execMeta.anyExecutionPassed = anyExecutionPassed.Load() == 1
				execMeta.anyExecutionFailed = anyExecutionFailed.Load() == 1
				syncFeatureMetadataFromExecution(ptrMeta, execMeta)
			},
			preIsLastRetry: func(execMeta *testExecutionMetadata, _ int, remainingRetries int64) bool {
				if execMeta.isAttemptToFix && ptrMeta.shouldOrchestrateAttemptToFix {
					// For attempt-to-fix tests and EFD, the last retry is when remaining retries == 1.
					return remainingRetries == 1
				}

				if isAnEfdExecution(execMeta) {
					// For EFD, the last retry is when remaining retries == 1.
					return remainingRetries == 1
				}

				// FlakyTestRetries also considers the global remaining retry count.
				if execMeta.isFlakyTestRetriesEnabled {
					return remainingRetries == 1 || atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount) == 0
				}

				return false
			},
			postAdjustRetryCount: func(execMeta *testExecutionMetadata, duration time.Duration) int64 {
				// adjust retry count only runs after the first run

				// Attempt To Fix retries are always set to the configured value.
				if execMeta.isAttemptToFix && ptrMeta.shouldOrchestrateAttemptToFix {
					if execMeta.identity != nil && len(execMeta.identity.Segments) > 1 {
						log.Debug("postAdjustRetryCount attempt_to_fix identity=%s setting=%d", execMeta.identity.FullName, settings.TestManagement.AttemptToFixRetries)
					}
					return int64(settings.TestManagement.AttemptToFixRetries)
				}

				// Early Flake Detection adjusts the retry count based on test duration.
				if isAnEfdExecution(execMeta) {
					slowTestRetries := settings.EarlyFlakeDetection.SlowTestRetries
					secs := duration.Seconds()
					if secs < 5 {
						return int64(slowTestRetries.FiveS)
					} else if secs < 10 {
						return int64(slowTestRetries.TenS)
					} else if secs < 30 {
						return int64(slowTestRetries.ThirtyS)
					} else if duration.Minutes() < 5 {
						return int64(slowTestRetries.FiveM)
					}
				}

				// Automatic flaky tests retries are set to the configured value.
				if execMeta.isFlakyTestRetriesEnabled {
					return integrations.GetFlakyRetriesSettings().RetryCount
				}

				// No retries
				return 0
			},
			postPerExecution: func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, executionIndex int, _ time.Duration) {
				failed := ptrToLocalT.Failed()
				skipped := ptrToLocalT.Skipped()
				log.Debug("applyAdditionalFeaturesToTestFunc: postPerExecution called for execution %d, failed: %t, skipped: %t", executionIndex, failed, skipped)

				if failed || skipped {
					atomic.StoreInt32(&allAttemptsPassed, 0)
				}
				if !failed {
					atomic.StoreInt32(&allRetriesFailed, 0)
				}

				// Track pass/fail for test.final_status computation.
				if !failed && !skipped {
					anyExecutionPassed.Store(1)
				}
				if failed {
					anyExecutionFailed.Store(1)
				}

				if execMeta.isAttemptToFix {
					status := "PASS"
					if failed {
						status = "FAIL"
					} else if skipped {
						status = "SKIP"
					}
					if execMeta.identity != nil && len(execMeta.identity.Segments) > 1 {
						log.Debug("postPerExecution attempt_to_fix identity=%s orchestrate=%t run=%d status=%s", execMeta.identity.FullName, ptrMeta.shouldOrchestrateAttemptToFix, executionIndex, status)
					}

					if ptrMeta.shouldOrchestrateAttemptToFix {
						isSubtest := execMeta.identity != nil && len(execMeta.identity.Segments) > 1
						if !isSubtest {
							ptrToLocalT.Logf("  [attempt to fix retry: %d (%s)]", executionIndex+1, status)
						}
					}
					return
				}

				if isAnEfdExecution(execMeta) {
					if skipped {
						log.Debug("applyAdditionalFeaturesToTestFunc: EFD test skipped, incrementing skip count")
						testSkipCount++
					} else if failed {
						log.Debug("applyAdditionalFeaturesToTestFunc: EFD test failed, incrementing fail count")
						testFailCount++
					} else {
						log.Debug("applyAdditionalFeaturesToTestFunc: EFD test passed, incrementing pass count")
						testPassCount++
					}
					return
				}

				if execMeta.isFlakyTestRetriesEnabled {
					return
				}
			},
			postShouldRetry: func(ptrToLocalT *testing.T, execMeta *testExecutionMetadata, _ int, remainingRetries int64) bool {
				if execMeta.isAttemptToFix && ptrMeta.shouldOrchestrateAttemptToFix {
					// For attempt-to-fix tests, retry if remaining retries > 0.
					return remainingRetries > 0
				}

				if isAnEfdExecution(execMeta) {
					// Clean skips do not add flakiness signal, so EFD stops before scheduling retries.
					cleanSkip := ptrToLocalT.Skipped() && !ptrToLocalT.Failed()
					return !cleanSkip && remainingRetries >= 0
				}

				if execMeta.isFlakyTestRetriesEnabled {
					return willRetryAfterExecution(
						ptrToLocalT.Failed(),
						ptrToLocalT.Skipped(),
						execMeta,
						remainingRetries,
						atomic.LoadInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount),
					)
				}

				// No retries for other cases.
				return false
			},
			postOnRetryEnd: func(t *testing.T, executionIndex int, lastPtrToLocalT *testing.T) {
				// if the test is disabled or quarantined, skip the test result to the testing framework
				if ptrMeta.isDisabled || ptrMeta.isQuarantined {
					log.Debug("applyAdditionalFeaturesToTestFunc: Skipping test result for disabled or quarantined test")
					t.SkipNow()
					return
				}

				// get the test common privates
				tCommonPrivates := getTestPrivateFields(t)
				if tCommonPrivates == nil {
					panic("getting test private fields failed")
				}

				// Attempt-to-fix owns result propagation when it is active, even if EFD or FTR
				// metadata is also present for tag compatibility.
				attemptToFixActive := ptrMeta.isAttemptToFix

				// if early flake detection is enabled, we need to set the test status
				efdOnNewTest := ptrMeta.isEarlyFlakeDetectionEnabled && ptrMeta.isNew && !attemptToFixActive
				efdOnModifiedTest := ptrMeta.isEarlyFlakeDetectionEnabled && ptrMeta.isModified && !attemptToFixActive
				if efdOnNewTest || efdOnModifiedTest {
					log.Debug("applyAdditionalFeaturesToTestFunc: Setting test status for Early Flake Detection")
					status := "passed"
					if testPassCount == 0 {
						if testSkipCount > 0 {
							status = "skipped"
							tCommonPrivates.SetSkipped(true)
						}
						if testFailCount > 0 {
							status = "failed"
							tCommonPrivates.SetFailed(true)
							tParentCommonPrivates := getTestParentPrivateFields(t)
							if tParentCommonPrivates == nil {
								panic("getting test parent private fields failed")
							}
							tParentCommonPrivates.SetFailed(true)
						}
					}
					if executionIndex > 0 {
						fmt.Printf("  [ %v after %v retries by Datadog's early flake detection ]\n", status, executionIndex)
					}
					return
				}

				// if the test is a flaky test retries test, we need to set the test status
				if ptrMeta.isFlakyTestRetriesEnabled && !attemptToFixActive {
					log.Debug("applyAdditionalFeaturesToTestFunc: Setting test status for Flaky Test Retries")
					tCommonPrivates.SetFailed(lastPtrToLocalT.Failed())
					tCommonPrivates.SetSkipped(lastPtrToLocalT.Skipped())
					if lastPtrToLocalT.Failed() {
						tParentCommonPrivates := getTestParentPrivateFields(t)
						if tParentCommonPrivates == nil {
							panic("getting test parent private fields failed")
						}
						tParentCommonPrivates.SetFailed(true)
					}
					if executionIndex > 0 {
						status := "passed"
						if t.Failed() {
							status = "failed"
						} else if t.Skipped() {
							status = "skipped"
						}
						fmt.Printf("    [ %v after %v retries by Datadog's auto test retries ]\n", status, executionIndex)
					}
					return
				}

				log.Debug("applyAdditionalFeaturesToTestFunc: Setting test status for regular test execution")
				tCommonPrivates.SetFailed(lastPtrToLocalT.Failed())
				tCommonPrivates.SetSkipped(lastPtrToLocalT.Skipped())
				if lastPtrToLocalT.Failed() {
					tParentCommonPrivates := getTestParentPrivateFields(t)
					if tParentCommonPrivates == nil {
						panic("getting test parent private fields failed")
					}
					tParentCommonPrivates.SetFailed(true)
				}
			},
		})
	}

	// Mark the wrapper as instrumented.
	setInstrumentationMetadata(runtime.FuncForPC(reflect.ValueOf(wrapper).Pointer()), &instrumentationMetadata{IsInternal: true})
	return wrapper
}

// runTestWithRetry encapsulates the common retry logic for test functions.
func runTestWithRetry(options *runTestWithRetryOptions) {
	// Set this func as a helper func of t
	options.t.Helper()

	// Initialize execution options variables
	execOpts := &executionOptions{
		mutex:                       &noopMutex{},
		options:                     options,
		parallelForwardState:        newParallelForwardState(),
		executionIndex:              -1,
		retryCount:                  int64(0),
		originalExecutionMetadata:   getTestMetadata(options.t),
		flakyRetryBudgetReservation: &flakyRetryBudgetReservation{},
	}
	defer refundFlakyRetryBudgetReservation(execOpts)
	prepareProcessRetryExecution(options, execOpts)

	// Execute the test function for the first time
	if executeTestIteration(execOpts) {
		// retry is required
		// In parallel, we use the retry count set in the first execution.
		calculatedRetryCount := execOpts.retryCount
		remainingAttempts := calculatedRetryCount + 1
		runSequentialRetries := func(stopOnProcessShutdown bool) {
			for {
				if stopOnProcessShutdown && processRetryShutdownRequested(execOpts.processRetryShutdown) {
					execOpts.retryCount = 0
					break
				}
				if !executeTestIteration(execOpts) {
					break
				}
			}
		}
		processHandled, reason := runProcessRetriesIfEligible(execOpts, runSequentialRetries)
		if processHandled {
			// The process backend, its fallback, or shutdown owns the remaining retries.
		} else if shouldUseParallelEFD(options, execOpts.executionMetadata, remainingAttempts, internalParallelEFDMaxConcurrency) {
			log.Debug("runTestWithRetry: process retry backend ineligible: %s", reason)
			log.Debug("runTestWithRetry: executing test in parallel EFD with retry count: %d and max concurrency: %d", calculatedRetryCount, internalParallelEFDMaxConcurrency)
			execOpts.mutex = newExecutionOptionsMutex()
			execOpts.effectiveParallelEFDActive = true
			runBoundedParallelEFDIterations(execOpts, remainingAttempts, internalParallelEFDMaxConcurrency)
		} else {
			// Execute retries sequentially
			runSequentialRetries(false)
		}
	}
	// Adjust execution metadata
	if execOpts.processRetryConsumedAttempt && options.preProcessRetryMetaAdjust != nil {
		options.preProcessRetryMetaAdjust(execOpts.executionMetadata, execOpts.executionIndex)
	} else if options.preExecMetaAdjust != nil {
		options.preExecMetaAdjust(execOpts.executionMetadata, execOpts.executionIndex)
	}

	// Call onRetryEnd
	if options.postOnRetryEnd != nil {
		options.postOnRetryEnd(options.t, execOpts.executionIndex, execOpts.ptrToLocalT)
	}

	// After all test executions, check if we need to close the suite and the module
	if execOpts.originalExecutionMetadata == nil {
		checkModuleAndSuite(execOpts.module, execOpts.suite)
	}

	// Re-panic if test failed and panic data exists
	if options.t.Failed() && execOpts.panicExecutionMetadata != nil {
		// Ensure we flush all CI visibility data and close the session event
		integrations.ExitCiVisibility()
		panic(fmt.Sprintf("test failed and panicked after %d retries.\n%v\n%v", execOpts.executionIndex, execOpts.panicExecutionMetadata.panicData, execOpts.panicExecutionMetadata.panicStacktrace))
	}
}

// shouldUseParallelEFD returns true only when the post-first-execution state qualifies for the parallel EFD scheduler.
func shouldUseParallelEFD(options *runTestWithRetryOptions, execMeta *testExecutionMetadata, remainingAttempts, maxConcurrency int64) bool {
	if options == nil || execMeta == nil {
		return false
	}
	if !options.parallelEFDAllowed {
		return false
	}
	if remainingAttempts <= 1 || maxConcurrency <= 1 {
		return false
	}
	if execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix {
		return false
	}
	return isAnEfdExecution(execMeta)
}

// runBoundedParallelEFDIterations schedules remaining EFD attempts while limiting concurrent retry executions.
func runBoundedParallelEFDIterations(execOpts *executionOptions, attempts, maxConcurrency int64) {
	if attempts <= 0 {
		return
	}
	parallelism := min(maxConcurrency, attempts)
	if parallelism <= 1 {
		for range attempts {
			executeTestIteration(execOpts)
		}
		return
	}

	sem := make(chan struct{}, int(parallelism))
	var wg sync.WaitGroup
	wg.Add(int(attempts))
	for range attempts {
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			executeTestIteration(execOpts)
		}()
	}
	wg.Wait()
}

// executeTestIteration runs a single attempt of the test (or subtest), recording metadata and
// ensuring the retry orchestration has the latest execution context.
func executeTestIteration(execOpts *executionOptions) bool {
	// Iteration lock
	execOpts.mutex.Lock()
	defer execOpts.mutex.Unlock()

	// Clear the matcher subnames map before each execution to avoid subname tests being called "parent/subname#NN" due to retries
	matcher := getTestContextMatcherPrivateFields(execOpts.options.t)
	if matcher != nil {
		matcher.ClearSubNames()
	}

	// Increment execution index
	execOpts.executionIndex++
	currentIndex := execOpts.executionIndex
	if currentIndex > 0 {
		consumeFlakyRetryBudgetReservation(execOpts)
	}

	// Create a new local copy of `t` to isolate execution results
	ptrToLocalT := createNewTest()
	copyTestWithoutParent(execOpts.options.t, ptrToLocalT)
	// Ensure cloned tests don't share the same output writer (Go 1.25+).
	reinitOutputWriter(ptrToLocalT)
	ptrToLocalT.Helper()
	execOpts.options.t.Helper()

	// Create a dummy parent so we can run the test using this local copy
	// without affecting the test parent
	localTPrivateFields := getTestPrivateFields(ptrToLocalT)
	if localTPrivateFields == nil {
		panic("getting test private fields failed")
	}
	if localTPrivateFields.parent == nil {
		panic("parent of the test is nil")
	}
	dummyParent := &testing.T{}
	copyTestWithoutParent(execOpts.options.t, dummyParent)
	// Ensure the dummy parent doesn't share the original test's output writer (Go 1.25+).
	reinitOutputWriter(dummyParent)
	*localTPrivateFields.parent = unsafe.Pointer(dummyParent)

	var cleanupResult testCleanupResult

	// Create an execution metadata instance
	execMeta := createTestMetadata(ptrToLocalT, execOpts.options.t)
	execMeta.flakyRetryBudgetReservation = execOpts.flakyRetryBudgetReservation
	execMeta.parallelForwardState = execOpts.parallelForwardState
	execMeta.hasAdditionalFeatureWrapper = true
	execMeta.cleanupResult = &cleanupResult

	// Propagate set tags from a parent wrapper
	propagateTestExecutionMetadataFlags(execMeta, execOpts.originalExecutionMetadata)

	// If we are in a retry execution, set the `isARetry` flag
	execMeta.isARetry = currentIndex > 0

	// Adjust execution metadata
	if execOpts.options.preExecMetaAdjust != nil {
		execOpts.options.preExecMetaAdjust(execMeta, currentIndex)
	}

	// Set if we are in the last retry
	if execMeta.isARetry {
		execMeta.isLastRetry = execOpts.options.preIsLastRetry(execMeta, currentIndex, execOpts.retryCount)
	}

	// Set remaining retries and parallel EFD flag for test.final_status computation.
	execMeta.remainingRetries = execOpts.retryCount
	execMeta.isEfdInParallel = execOpts.effectiveParallelEFDActive && isAnEfdExecution(execMeta)

	// unlock the mutex
	execOpts.mutex.Unlock()

	// Run original func similar to how it gets run internally in tRunner
	startTime := time.Now()
	duration := time.Duration(0)
	chn := make(chan struct{}, 1)
	go func(pLocalT *testing.T, opts *runTestWithRetryOptions, cn *chan struct{}) {
		defer func() {
			*cn <- struct{}{}
		}()
		defer func() {
			completeParallelSubtests(pLocalT, localTPrivateFields, false)
		}()
		defer func() {
			duration = time.Since(startTime)
		}()
		defer func() {
			if !cleanupResult.ran {
				runTestCleanup(pLocalT, &cleanupResult)
			}
		}()
		bodyReturned := false
		defer func() {
			if !processRetryUnexpectedTestTermination(pLocalT, bodyReturned) {
				return
			}
			pLocalT.Fail()
			if execMeta.panicData == nil {
				execMeta.panicData = unexpectedTestTerminationMessage
				execMeta.panicStacktrace = utils.GetStacktrace(1)
			}
		}()
		pLocalT.Helper()
		opts.t.Helper()
		opts.targetFunc(pLocalT)
		bodyReturned = true
	}(ptrToLocalT, execOpts.options, &chn)
	<-chn

	// Lock mutex
	execOpts.mutex.Lock()

	// Copy the current test to the wrapper if necessary
	if execOpts.originalExecutionMetadata != nil {
		execOpts.originalExecutionMetadata.test = execMeta.test
	}

	// Extract module and suite if present
	if execMeta.test == nil && execMeta.identity != nil {
		log.Debug("execMeta.test nil for %s", execMeta.identity.FullName)
	}
	var currentSuite integrations.TestSuite
	if execMeta.test != nil {
		currentSuite = execMeta.test.Suite()
	}
	if execOpts.suite == nil && currentSuite != nil {
		execOpts.suite = currentSuite
	}
	if execOpts.module == nil && currentSuite != nil && currentSuite.Module() != nil {
		execOpts.module = currentSuite.Module()
	}

	// Remove execution metadata
	deleteTestMetadata(ptrToLocalT)

	// Handle panic data
	if execMeta.panicData != nil {
		ptrToLocalT.Fail()
		if execOpts.panicExecutionMetadata == nil {
			execOpts.panicExecutionMetadata = execMeta
		}
	}
	applyTestCleanupResult(ptrToLocalT, execMeta, &cleanupResult)
	if cleanupResult.panicData != nil && execOpts.panicExecutionMetadata == nil {
		execOpts.panicExecutionMetadata = execMeta
	}

	// Adjust retry count after first execution if necessary
	if execOpts.options.postAdjustRetryCount != nil && currentIndex == 0 {
		execOpts.retryCount = execOpts.options.postAdjustRetryCount(execMeta, duration)
	}

	// Decrement retry count
	execOpts.retryCount--

	// Call perExecution function
	if execOpts.options.postPerExecution != nil {
		execOpts.options.postPerExecution(ptrToLocalT, execMeta, currentIndex, duration)
	}

	// Update lastPtrToLocalT and lastExecMeta
	execOpts.ptrToLocalT = ptrToLocalT
	execOpts.executionMetadata = execMeta

	// Decide whether to continue
	return reserveRetryBudgetIfNeeded(execOpts, ptrToLocalT, execMeta, currentIndex)
}

func reserveRetryBudgetIfNeeded(execOpts *executionOptions, t *testing.T, execMeta *testExecutionMetadata, executionIndex int) bool {
	if usesFlakyRetryBudget(execMeta) && execMeta.flakyRetryBudgetReservation != nil && execMeta.flakyRetryBudgetReservation.reserved() {
		return true
	}
	if !execOpts.options.postShouldRetry(t, execMeta, executionIndex, execOpts.retryCount) {
		return false
	}
	if !usesFlakyRetryBudget(execMeta) {
		return true
	}
	if execOpts.flakyRetryBudgetReservation == nil {
		execOpts.flakyRetryBudgetReservation = &flakyRetryBudgetReservation{}
	}
	if !execOpts.flakyRetryBudgetReservation.reserve() {
		return false
	}
	return true
}

func usesFlakyRetryBudget(execMeta *testExecutionMetadata) bool {
	return execMeta != nil && execMeta.isFlakyTestRetriesEnabled && !execMeta.isAttemptToFix && !isAnEfdExecution(execMeta)
}

func consumeFlakyRetryBudgetReservation(execOpts *executionOptions) {
	if execOpts == nil {
		return
	}
	if execOpts.flakyRetryBudgetReservation != nil {
		execOpts.flakyRetryBudgetReservation.consume()
	}
	execOpts.flakyRetryBudgetReservation = &flakyRetryBudgetReservation{}
}

func refundFlakyRetryBudgetReservation(execOpts *executionOptions) {
	if execOpts == nil || execOpts.flakyRetryBudgetReservation == nil {
		return
	}
	execOpts.flakyRetryBudgetReservation.refund()
}

const (
	flakyRetryBudgetIdle int32 = iota
	flakyRetryBudgetReserving
	flakyRetryBudgetReserved
	flakyRetryBudgetConsumed
	flakyRetryBudgetRefunded
)

func (r *flakyRetryBudgetReservation) reserve() bool {
	if r == nil {
		return false
	}
	for {
		switch r.state.Load() {
		case flakyRetryBudgetReserved:
			return true
		case flakyRetryBudgetConsumed, flakyRetryBudgetRefunded:
			return false
		case flakyRetryBudgetReserving:
			runtime.Gosched()
		case flakyRetryBudgetIdle:
			if !r.state.CompareAndSwap(flakyRetryBudgetIdle, flakyRetryBudgetReserving) {
				continue
			}
			if !tryReserveFlakyRetryBudget() {
				r.state.Store(flakyRetryBudgetIdle)
				return false
			}
			r.state.Store(flakyRetryBudgetReserved)
			return true
		}
	}
}

func (r *flakyRetryBudgetReservation) reserved() bool {
	return r != nil && r.state.Load() == flakyRetryBudgetReserved
}

func (r *flakyRetryBudgetReservation) consume() {
	if r != nil {
		r.state.CompareAndSwap(flakyRetryBudgetReserved, flakyRetryBudgetConsumed)
	}
}

func (r *flakyRetryBudgetReservation) refund() {
	if r == nil || !r.state.CompareAndSwap(flakyRetryBudgetReserved, flakyRetryBudgetRefunded) {
		return
	}
	atomic.AddInt64(&integrations.GetFlakyRetriesSettings().RemainingTotalRetryCount, 1)
}

// runTestCleanup executes testing cleanups for a retry attempt. It isolates
// cleanup Goexit in a helper goroutine so retry orchestration can treat cleanup
// failures as attempt failures instead of letting them escape the retry loop.
func runTestCleanup(t *testing.T, result *testCleanupResult) {
	runTestCleanupWithOptions(t, result, false)
}

func runTestCleanupWithOptions(t *testing.T, result *testCleanupResult, neutralizeNativeParallelRelease bool) {
	completeParallelSubtests(t, getTestPrivateFields(t), neutralizeNativeParallelRelease)
	result.ran = true
	done := make(chan struct{})
	go func() {
		completed := false
		defer func() {
			if !completed {
				result.goexit = true
			}
			close(done)
		}()
		result.panicData = testingTRunCleanup(t, 1)
		if result.panicData != nil {
			result.panicStacktrace = utils.GetStacktrace(1)
		}
		completed = true
	}()
	<-done
}

// completeParallelSubtests releases and waits for parallel subtests owned by a
// Datadog-managed clone. It mirrors testing.tRunner's scheduler accounting:
// release the parent slot before unblocking children, then reacquire it for
// sequential parents before running cleanup.
func completeParallelSubtests(t *testing.T, localTPrivateFields *commonPrivateFields, neutralizeNativeParallelRelease bool) {
	if localTPrivateFields == nil || localTPrivateFields.sub == nil || len(*localTPrivateFields.sub) == 0 {
		return
	}

	subtests := *localTPrivateFields.sub
	parentIsParallel := isParallelTest(t, localTPrivateFields)
	*localTPrivateFields.sub = nil
	testState := getTestState(t)
	if testState != nil {
		testingTestStateRelease(testState)
	}
	if localTPrivateFields.barrier != nil && *localTPrivateFields.barrier != nil {
		close(*localTPrivateFields.barrier)
	}
	for _, sub := range subtests {
		pvSub := getTestPrivateFields(sub)
		if pvSub != nil && pvSub.signal != nil {
			<-*pvSub.signal
		}
	}
	if testState != nil && !parentIsParallel {
		testingTestStateWaitParallel(testState)
	}
	if neutralizeNativeParallelRelease && parentIsParallel && localTPrivateFields.isParallel != nil {
		// A process-retry child drains native tRunner subtests before writing
		// JSON. After we clear t.sub, Go's native tRunner would otherwise take
		// its len(t.sub)==0 && t.isParallel release path and release the same
		// scheduler slot twice.
		*localTPrivateFields.isParallel = false
	}
}

// isParallelTest reports whether the active test has entered Go's parallel-test
// path. Datadog-managed retry clones forward Parallel to the original *testing.T,
// so the original must also be checked before deciding whether to reacquire the
// scheduler slot.
func isParallelTest(t *testing.T, localTPrivateFields *commonPrivateFields) bool {
	if localTPrivateFields != nil && localTPrivateFields.isParallel != nil && *localTPrivateFields.isParallel {
		return true
	}
	if execMeta := getTestMetadata(t); execMeta != nil && execMeta.originalTest != nil {
		originalFields := getTestPrivateFields(execMeta.originalTest)
		return originalFields != nil && originalFields.isParallel != nil && *originalFields.isParallel
	}
	return false
}

// runAndApplyTestCleanup runs a retry attempt's cleanups before its span is
// finalized, then applies any cleanup failure to the attempt metadata.
func runAndApplyTestCleanup(t *testing.T, execMeta *testExecutionMetadata) {
	if execMeta == nil || execMeta.cleanupResult == nil || execMeta.cleanupResult.ran {
		return
	}
	runTestCleanup(t, execMeta.cleanupResult)
	applyTestCleanupResult(t, execMeta, execMeta.cleanupResult)
}

// applyTestCleanupResult applies cleanup panics and failing Goexit results to
// the test attempt so retry orchestration can make the normal retry decision.
// Cleanup SkipNow also exits with Goexit, but testing treats that as a skipped
// test when it is not already failed, so it must keep its skipped status.
func applyTestCleanupResult(t *testing.T, execMeta *testExecutionMetadata, result *testCleanupResult) {
	if result == nil || (result.panicData == nil && !result.goexit) {
		return
	}
	if result.panicData == nil && result.goexit && t.Skipped() && !t.Failed() {
		return
	}
	t.Fail()
	if result.panicData == nil {
		return
	}
	execMeta.panicData = result.panicData
	execMeta.panicStacktrace = result.panicStacktrace
}

// propagateTestExecutionMetadataFlags propagates the test execution metadata flags from the original test execution metadata to the current one.
func propagateTestExecutionMetadataFlags(execMeta *testExecutionMetadata, originalExecMeta *testExecutionMetadata) {
	if execMeta == nil || originalExecMeta == nil {
		return
	}

	// Propagate the test execution metadata
	execMeta.isANewTest = execMeta.isANewTest || originalExecMeta.isANewTest
	execMeta.isAModifiedTest = execMeta.isAModifiedTest || originalExecMeta.isAModifiedTest
	execMeta.isEarlyFlakeDetectionEnabled = execMeta.isEarlyFlakeDetectionEnabled || originalExecMeta.isEarlyFlakeDetectionEnabled
	execMeta.isFlakyTestRetriesEnabled = execMeta.isFlakyTestRetriesEnabled || originalExecMeta.isFlakyTestRetriesEnabled
	if execMeta.flakyRetryBudgetReservation == nil {
		execMeta.flakyRetryBudgetReservation = originalExecMeta.flakyRetryBudgetReservation
	}
	execMeta.isQuarantined = execMeta.isQuarantined || originalExecMeta.isQuarantined
	execMeta.isDisabled = execMeta.isDisabled || originalExecMeta.isDisabled
	if !execMeta.suppressParentRetryMetadata {
		execMeta.isARetry = execMeta.isARetry || originalExecMeta.isARetry
		execMeta.isEfdInParallel = execMeta.isEfdInParallel || originalExecMeta.isEfdInParallel
		execMeta.hasAdditionalFeatureWrapper = execMeta.hasAdditionalFeatureWrapper || originalExecMeta.hasAdditionalFeatureWrapper
	}
	if !execMeta.hasExplicitAttemptToFix && originalExecMeta.isAttemptToFix {
		// Preserve attempt-to-fix inheritance only when the child didn't explicitly override it.
		execMeta.isAttemptToFix = true
	}
}

// isAnEfdExecution checks if the current test execution is an Early Flake Detection execution.
func isAnEfdExecution(execMeta *testExecutionMetadata) bool {
	isANewTest := execMeta.isANewTest
	isAModifiedTest := execMeta.isAModifiedTest && !execMeta.isAttemptToFix
	return execMeta.isEarlyFlakeDetectionEnabled && (isANewTest || isAModifiedTest)
}

type noopMutex struct{}

func (m *noopMutex) Lock()         {}
func (m *noopMutex) Unlock()       {}
func (m *noopMutex) TryLock() bool { return true }

func newExecutionOptionsMutex() sync.Locker {
	return &locking.Mutex{}
}

//go:linkname testingTRunCleanup testing.(*common).runCleanup
func testingTRunCleanup(c *testing.T, ph int) (panicVal any)

//go:linkname testingTestStateWaitParallel testing.(*testState).waitParallel
func testingTestStateWaitParallel(s *testingTestState)

//go:linkname testingTestStateRelease testing.(*testState).release
func testingTestStateRelease(s *testingTestState)
