// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/logs"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// testFramework represents the name of the testing framework.
	testFramework = "golang.org/pkg/testing"
)

var (
	// session represents the CI visibility test session.
	session integrations.TestSession

	// testInfos holds information about the instrumented tests.
	testInfos []*testingTInfo

	// benchmarkInfos holds information about the instrumented benchmarks.
	benchmarkInfos []*testingBInfo

	// modulesCountersMutex is a mutex to protect access to the modulesCounters map.
	modulesCountersMutex sync.Mutex

	// modulesCounters keeps track of the number of tests per module.
	modulesCounters = map[string]int{}

	// suitesCountersMutex is a mutex to protect access to the suitesCounters map.
	suitesCountersMutex sync.Mutex

	// suitesCounters keeps track of the number of tests per suite.
	suitesCounters = map[string]int{}

	// numOfTestsSkipped keeps track of the number of tests skipped by ITR.
	numOfTestsSkipped atomic.Uint64

	// chattyPrinterOnce ensures that the chatty printer is initialized only once.
	chattyPrinterOnce sync.Once

	// chatty is the global chatty printer used for debugging and verbose output.
	chatty *chattyPrinter

	testingMInstrumentationClaims = struct {
		mu      locking.Mutex
		claimed map[*testing.M]*testingMInstrumentationClaim
	}{claimed: make(map[*testing.M]*testingMInstrumentationClaim)}

	testingBuiltWithOrchestrion atomic.Bool
	testingMRunEpochCounter     atomic.Uint64
	testingMActiveHookEpoch     atomic.Pointer[testingMHookEpoch]
)

type (
	testingMFinalizer func(exitCode int) int

	testingMHookEpoch struct {
		id      uint64
		active  atomic.Int64
		closing atomic.Bool
		drained chan struct{}
		once    sync.Once
	}

	testingMInstrumentationClaim struct {
		tests                map[string]func(*testing.T)
		benchmarks           map[string]func(*testing.B)
		testDescriptors      *[]testing.InternalTest
		benchmarkDescriptors *[]testing.InternalBenchmark
		retired              bool
	}

	// testIdentity represents the fully-qualified identity of a Go test or subtest.
	// It captures the module and suite where the test belongs, the base test name
	// (top-level test), the full hierarchical name reported by Go (including subtests),
	// and every individual path segment in order. This allows test management logic to
	// resolve configuration at any depth while still falling back to parent segments.
	testIdentity struct {
		ModuleName string
		SuiteName  string
		BaseName   string
		FullName   string
		Segments   []string
	}

	// commonInfo holds common information about tests and benchmarks.
	commonInfo struct {
		moduleName string
		suiteName  string
		testName   string
		identity   *testIdentity
		sourceFunc *runtime.Func
	}

	// testingTInfo holds information specific to tests.
	testingTInfo struct {
		commonInfo
		originalFunc func(*testing.T)
	}

	// testingBInfo holds information specific to benchmarks.
	testingBInfo struct {
		commonInfo
		originalFunc func(b *testing.B)
	}

	// M is a wrapper around testing.M to provide instrumentation.
	M testing.M
)

// newTestIdentity builds a testIdentity instance for the provided module, suite,
// and fully-qualified Go test name. The base name corresponds to the first path
// segment (the top-level test declared via testing.T.Run). The Segments slice
// always contains at least one entry so consumers can traverse parent levels.
func newTestIdentity(moduleName, suiteName, fullName string) *testIdentity {
	if fullName == "" {
		fullName = "<unknown>"
	}
	segments := strings.Split(fullName, "/")
	baseName := segments[0]
	return &testIdentity{
		ModuleName: moduleName,
		SuiteName:  suiteName,
		BaseName:   baseName,
		FullName:   fullName,
		Segments:   segments,
	}
}

type testManagementMatchKind int

const (
	testManagementMatchNone testManagementMatchKind = iota
	testManagementMatchExact
	testManagementMatchAncestor
)

type testingMClaimDisposition uint8

const (
	testingMClaimOwner testingMClaimDisposition = iota
	testingMClaimActiveConflict
	testingMClaimRetiredNative
)

func claimTestingMInstrumentation(m *testing.M) (*testingMInstrumentationClaim, testingMClaimDisposition) {
	if m == nil {
		return &testingMInstrumentationClaim{}, testingMClaimOwner
	}
	testingMInstrumentationClaims.mu.Lock()
	defer testingMInstrumentationClaims.mu.Unlock()
	if claim := testingMInstrumentationClaims.claimed[m]; claim != nil {
		if claim.retired {
			return claim, testingMClaimRetiredNative
		}
		return claim, testingMClaimActiveConflict
	}
	claim := &testingMInstrumentationClaim{}
	testingMInstrumentationClaims.claimed[m] = claim
	return claim, testingMClaimOwner
}

func markTestingBuiltWithOrchestrion() {
	testingBuiltWithOrchestrion.Store(true)
}

func isTestingBuiltWithOrchestrion() bool {
	return testingBuiltWithOrchestrion.Load()
}

func activateTestingMHookEpoch(epoch uint64) func() {
	if epoch == 0 {
		epoch = testingMRunEpochCounter.Add(1)
	}
	state := &testingMHookEpoch{id: epoch, drained: make(chan struct{})}
	testingMActiveHookEpoch.Store(state)
	return func() {
		state.closing.Store(true)
		testingMActiveHookEpoch.CompareAndSwap(state, nil)
		state.signalDrained()
		<-state.drained
	}
}

func acquireOrchestrionTestingHook() (func(), bool) {
	if !isTestingBuiltWithOrchestrion() {
		return func() {}, true
	}
	state := testingMActiveHookEpoch.Load()
	if state == nil {
		return nil, false
	}
	release, ok := state.acquire()
	if !ok {
		return nil, false
	}
	if testingMActiveHookEpoch.Load() != state {
		release()
		return nil, false
	}
	return release, true
}

func (e *testingMHookEpoch) acquire() (func(), bool) {
	if e.closing.Load() {
		return nil, false
	}
	e.active.Add(1)
	if e.closing.Load() {
		e.release()
		return nil, false
	}
	return e.release, true
}

func (e *testingMHookEpoch) release() {
	if e.active.Add(-1) < 0 {
		panic("negative Orchestrion testing hook lease count")
	}
	e.signalDrained()
}

func (e *testingMHookEpoch) signalDrained() {
	if e.closing.Load() && e.active.Load() == 0 {
		e.once.Do(func() { close(e.drained) })
	}
}

func instrumentTestingMWithOptions(m *testing.M, wrapperOpts additionalFeatureWrapperOptions) (bool, testingMFinalizer) {
	if isProcessRetryChild() {
		cfg, err := bootstrapProcessRetryChild()
		if err != nil {
			reason := processRetryChildConfigErrorReason(err)
			log.Debug("civisibility: process retry child config error: %s", reason)
			writeInvalidProcessRetryChildConfigResult(cfg, reason)
			if !disableProcessRetryChildExecution(m) {
				hardStopInvalidProcessRetryChild("testing_m_reflection_drift")
			}
			return false, failureTestingMFinalizer
		}
		return instrumentProcessRetryChild(m, cfg)
	}
	claim, disposition := claimTestingMInstrumentation(m)
	switch disposition {
	case testingMClaimActiveConflict:
		return false, failureTestingMFinalizer
	case testingMClaimRetiredNative:
		return true, identityTestingMFinalizer
	}

	// Check if CI Visibility was disabled using the kill switch before trying to initialize it
	atomic.StoreInt32(&ciVisibilityEnabledValue, -1)
	if !isCiVisibilityEnabled() || !testing.Testing() {
		retireTestingMInstrumentation(m, claim)
		return true, identityTestingMFinalizer
	}
	if wrapperOpts.mRunEpoch == 0 {
		wrapperOpts.mRunEpoch = testingMRunEpochCounter.Add(1)
	}
	if wrapperOpts.mRunInvocations == nil {
		wrapperOpts.mRunInvocations = &atomic.Uint64{}
	}
	releaseHookEpoch := activateTestingMHookEpoch(wrapperOpts.mRunEpoch)

	log.Debug("instrumentTestingM: initializing CI Visibility for testing.M")

	// Initialize CI Visibility
	integrations.EnsureCiVisibilityInitialization()

	// Create a new test session for CI visibility.
	session = integrations.CreateTestSession(integrations.WithTestSessionFramework(testFramework, runtime.Version()))
	if wrapperOpts.processRetryAllowed && !registerProcessRetryShutdownAction() {
		log.Debug("instrumentTestingM: process retry shutdown action registration failed; falling back to in-process retries")
		wrapperOpts.processRetryAllowed = false
	}

	coverageInitialized := false
	settings := integrations.GetSettings()
	if settings != nil {
		if settings.CodeCoverage {
			// Initialize the runtime coverage if enabled.
			coverage.InitializeCoverage(m, true)
			coverageInitialized = true
		}
		if settings.TestManagement.Enabled && internal.BoolEnv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, true) {
			// Set the test management tag if enabled.
			session.SetTag(constants.TestManagementEnabled, "true")
		}
	}

	// Check if the coverage was enabled by not initialized
	if !coverageInitialized && testing.CoverMode() != "" {
		coverage.InitializeCoverage(m, false)
	}

	ddm := (*M)(m)

	// Instrument the internal tests for CI visibility.
	ddm.instrumentInternalTests(getInternalTestArray(m), wrapperOpts, claim)

	// Instrument the internal benchmarks for CI visibility.
	for _, v := range os.Args {
		// check if benchmarking is enabled to instrument
		if strings.Contains(v, "-bench") || strings.Contains(v, "test.bench") {
			ddm.instrumentInternalBenchmarks(getInternalBenchmarkArray(m), claim)
			break
		}
	}

	return true, func(exitCode int) int {
		retireTestingMInstrumentation(m, claim)
		releaseHookEpoch()
		log.Debug("instrumentTestingM: finished with exit code: %d", exitCode)

		// Check for code coverage if enabled.
		if testing.CoverMode() != "" {
			cov, corrected, publishCoverage := finalizeITRCoverageBackfill()
			uploadFinalCoverageReport(settings)
			if !publishCoverage {
				session.Close(exitCode)
				coverage.CleanupRuntimeCoverageSnapshot()
				integrations.ExitCiVisibility()
				return exitCode
			}
			if !corrected {
				// let's try first with our coverage package
				cov = coverage.GetCoverage()
			}
			if cov == 0 {
				// if not we try we the default testing package
				cov = testing.Coverage()
			}

			coveragePercentage := cov * 100
			session.SetTag(constants.CodeCoveragePercentageOfTotalLines, coveragePercentage)
		}

		// Close the session and return the exit code.
		session.Close(exitCode)
		coverage.CleanupRuntimeCoverageSnapshot()

		// Finalize CI Visibility
		integrations.ExitCiVisibility()
		return exitCode
	}
}

func identityTestingMFinalizer(exitCode int) int {
	return exitCode
}

func failureTestingMFinalizer(int) int {
	return processRetryFailureExitCode
}

func uploadFinalCoverageReport(settings *net.SettingsResponseData) {
	if settings == nil {
		log.Debug("instrumentTestingM: coverage report upload skipped because settings are unavailable")
		return
	}
	if !settings.CoverageReportUploadEnabled {
		log.Debug("instrumentTestingM: coverage report upload disabled by settings")
		return
	}

	var report bytes.Buffer
	if err := coverage.WriteLCOVReport(&report); err != nil {
		log.Debug("instrumentTestingM: failed to create LCOV coverage report: %s", err.Error())
		return
	}
	if report.Len() == 0 {
		log.Debug("instrumentTestingM: LCOV coverage report is empty; skipping upload")
		return
	}
	log.Debug("instrumentTestingM: uploading LCOV coverage report [report_bytes:%d]", report.Len())

	client := net.NewClientForCoverageReportUpload()
	if client == nil {
		log.Debug("instrumentTestingM: coverage report upload client is unavailable")
		return
	}
	if closer, ok := client.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	if err := client.SendCoverageReport(&report, net.FormatLCOV); err != nil {
		log.Debug("instrumentTestingM: failed to upload coverage report: %s", err.Error())
	}
}

// Run initializes CI Visibility, instruments tests and benchmarks, and runs them.
func (ddm *M) Run() (exitCode int) {
	m := (*testing.M)(ddm)
	if isTestingBuiltWithOrchestrion() {
		// The woven testing.M.Run entry owns both parent instrumentation and the
		// process-child admission gate. Claiming here as well would give one
		// native invocation two competing owners.
		return m.Run()
	}
	if isProcessRetryChild() {
		return runProcessRetryChild(m)
	}

	// Instrument testing.M
	proceed, exitFn := instrumentTestingMWithOptions(m, processRetryWrapperOptions())
	if !proceed {
		return exitFn(processRetryFailureExitCode)
	}

	// Finalization also restores the native workload descriptors if M.Run
	// unwinds through a panic instead of returning normally.
	defer func() { exitCode = exitFn(exitCode) }()
	exitCode = m.Run()
	return
}

func processRetryWrapperOptions() additionalFeatureWrapperOptions {
	return additionalFeatureWrapperOptions{
		processRetryAllowed: true,
		fuzzActive:          processRetryFuzzActive,
	}
}

// instrumentInternalTests instruments the internal tests for CI visibility.
func (ddm *M) instrumentInternalTests(internalTests *[]testing.InternalTest, wrapperOpts additionalFeatureWrapperOptions, claim *testingMInstrumentationClaim) {
	if internalTests == nil {
		return
	}
	if claim != nil {
		claim.testDescriptors = internalTests
	}

	// Get the settings response for this session
	settings := integrations.GetSettings()
	itrState := newITRState(settings)

	// Extract info from internal tests
	testInfos = make([]*testingTInfo, len(*internalTests))
	for idx, test := range *internalTests {
		moduleName, suiteName := utils.GetModuleAndSuiteName(reflect.Indirect(reflect.ValueOf(test.F)).Pointer())
		identity := newTestIdentity(moduleName, suiteName, test.Name)
		testInfo := &testingTInfo{
			originalFunc: test.F,
			commonInfo: commonInfo{
				moduleName: moduleName,
				suiteName:  suiteName,
				testName:   test.Name,
				identity:   identity,
			},
		}

		// Increment the test count in the module.
		addModulesCounters(moduleName, 1)

		// Increment the test count in the suite.
		addSuitesCounters(suiteName, 1)

		testInfos[idx] = testInfo
	}
	itrState.validateCoverageBackfillScope(testInfos)

	// Check if the test is going to be skipped by ITR
	if settings != nil && settings.ItrEnabled {
		coverageEnabled := coverage.CanCollectPerTestCoverage()
		session.SetTag(constants.CodeCoverageEnabled, strconv.FormatBool(coverageEnabled))
		testsSkippingEnabled := strconv.FormatBool(itrState.testsSkippingEnabled())
		session.SetTag(constants.ITRTestsSkippingEnabled, testsSkippingEnabled)
		utils.AddCITagsMap(map[string]string{constants.ITRTestsSkippingEnabled: testsSkippingEnabled})

		if itrState.testsSkippingEnabled() {
			session.SetTag(constants.ITRTestsSkippingType, "test")

			// Check if the test is going to be skipped by ITR
			if itrState.hasSkippableTests() {
				session.SetTag(constants.ITRTestsSkipped, "false")
			}
		}
	}

	// Create new instrumented internal tests
	newTestArray := make([]testing.InternalTest, len(*internalTests))
	for idx, testInfo := range testInfos {
		instrumented := ddm.executeInternalTest(testInfo, wrapperOpts)
		newTestArray[idx] = testing.InternalTest{
			Name: testInfo.testName,
			F:    instrumented,
		}
		if claim != nil {
			if claim.tests == nil {
				claim.tests = make(map[string]func(*testing.T), len(testInfos))
			}
			claim.tests[testInfo.testName] = testInfo.originalFunc
		}
	}
	*internalTests = newTestArray
}

// executeInternalTest wraps the original test function to include CI visibility instrumentation.
func (ddm *M) executeInternalTest(testInfo *testingTInfo, wrapperOpts additionalFeatureWrapperOptions) func(*testing.T) {
	originalFunc := runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(testInfo.originalFunc)).Pointer())
	testInfo.commonInfo.sourceFunc = originalFunc

	// Get the settings response for this session
	settings := integrations.GetSettings()
	coverageEnabled := coverage.CanCollectPerTestCoverage()
	testIsNew := true

	// Check if the test is known
	if settings.KnownTestsEnabled {
		testIsKnown, testKnownDataOk := isKnownTest(&testInfo.commonInfo)
		testIsNew = testKnownDataOk && !testIsKnown
	} else {
		// We don't mark any test as new if the feature is disabled
		testIsNew = false
	}

	// Instrument the test function
	instrumentedFunc := func(t *testing.T) {
		// Set this func as a helper func of t
		t.Helper()

		// Get the metadata regarding the execution (in case is already created from the additional features)
		execMeta := getTestMetadata(t)
		if execMeta == nil {
			// in case there's no additional features then we create the metadata for this execution and defer the disposal
			execMeta = createTestMetadata(t, nil)
			defer deleteTestMetadata(t)
		}
		execMeta.identity = testInfo.identity

		// Create or retrieve the module, suite, and test for CI visibility.
		module := session.GetOrCreateModule(testInfo.moduleName)
		suite := module.GetOrCreateSuite(testInfo.suiteName)
		test := suite.CreateTest(testInfo.testName)
		test.SetTestFunc(originalFunc)

		// If the execution is for a new test we tag the test event as new
		execMeta.isANewTest = execMeta.isANewTest || testIsNew

		// Set some required tags from the execution metadata
		cancelExecution := setTestTagsFromExecutionMetadata(test, execMeta)
		if cancelExecution {
			if !execMeta.hasAdditionalFeatureWrapper {
				// Disabled fast-path executions close their test event before the normal defer is registered.
				checkModuleAndSuite(module, suite)
			}
			return
		}

		// Check if the test needs to be skipped by ITR after all execution
		// metadata has been attached, but before coverage and user code run.
		itrDecision := currentITRState().decisionFor(testInfo, execMeta, test.Context().Value(constants.TestUnskippable) == true)
		if itrDecision.skip {
			test.SetTag(constants.TestSkippedByITR, "true")
			// ITR skip is always a final execution, set the final status
			test.SetTag(constants.TestFinalStatus, constants.TestStatusSkip)
			test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(constants.SkippedByITRReason))
			telemetry.ITRSkipped(telemetry.TestEventType)
			currentITRState().markActualSkip()
			session.SetTag(constants.ITRTestsSkipped, "true")
			session.SetTag(constants.ITRTestsSkippingCount, numOfTestsSkipped.Add(1))
			if !execMeta.hasAdditionalFeatureWrapper {
				checkModuleAndSuite(module, suite)
			}
			t.Skip(constants.SkippedByITRReason)
			return
		}
		if itrDecision.forcedRun {
			execMeta.isItrForcedRun = true
			test.SetTag(constants.TestForcedToRun, "true")
			telemetry.ITRForcedRun(telemetry.TestEventType)
		}

		// Check if the coverage is enabled
		var tCoverage coverage.TestCoverage
		var tParentOldBarrier chan bool
		if shouldCollectExecutionCoverage(coverageEnabled, execMeta) && coverage.CanCollect() {
			// set the test coverage collector
			testFile, _ := originalFunc.FileLine(originalFunc.Entry())
			tCoverage = coverage.NewTestCoverage(
				session.SessionID(),
				module.ModuleID(),
				suite.SuiteID(),
				test.TestID(),
				testFile)

			// now we need to disable parallelism for the test in order to collect the test coverage
			tParent := getTestParentPrivateFields(t)
			if tParent != nil && tParent.barrier != nil {
				tParentOldBarrier = *tParent.barrier
				*tParent.barrier = nil
			}
		}

		// Initialize the chatty printer if not already done.
		instrumentChattyPrinter(t)

		startTime := time.Now()
		bodyReturned := false
		defer func() {
			r := recover()
			bodyDuration := time.Since(startTime)

			if tCoverage != nil {
				// Collect coverage after test execution so we can calculate the diff comparing to the baseline.
				tCoverage.CollectCoverageAfterTestExecution()

				// now we restore the original parent barrier
				tParent := getTestParentPrivateFields(t)
				if tParent != nil && tParent.barrier != nil {
					*tParent.barrier = tParentOldBarrier
				}
			}

			if execMeta.usesFreshRetryAttemptRuntime {
				bodyTerminal := r
				bodyStack := ""
				if bodyTerminal != nil {
					bodyStack = utils.GetStacktrace(1)
				}
				execMeta.retryAttemptFinalizer = func(result retryAttemptResult) {
					terminal := bodyTerminal
					terminalStack := bodyStack
					if result.panicData != nil {
						terminal = result.panicData
						terminalStack = string(result.panicStack)
					}
					if result.cleanupPanicData != nil {
						terminal = result.cleanupPanicData
						terminalStack = string(result.cleanupPanicStack)
					}
					finalizeInstrumentedTestExecution(t, execMeta, test, suite, module, result.duration, result.output, terminal, terminalStack, true)
				}
				if r != nil {
					panic(r)
				}
				return
			}

			unexpectedTermination := r == nil && processRetryUnexpectedTestTermination(t, bodyReturned)
			duration := runAndApplyTestCleanupWithDuration(t, execMeta, bodyDuration)
			if unexpectedTermination {
				r = unexpectedTestTerminationMessage
			}
			terminalStack := ""
			if r != nil {
				terminalStack = utils.GetStacktrace(1)
			}
			finalizeInstrumentedTestExecution(t, execMeta, test, suite, module, duration, nil, r, terminalStack, true)
			if r != nil && !execMeta.hasAdditionalFeatureWrapper {
				checkModuleAndSuite(module, suite)
				integrations.ExitCiVisibility()
				panic(r)
			}
			if r == nil && !execMeta.hasAdditionalFeatureWrapper {
				checkModuleAndSuite(module, suite)
			}
		}()

		if tCoverage != nil {
			// Collect coverage before test execution so we can register a baseline.
			tCoverage.CollectCoverageBeforeTestExecution()
		}

		// A masking capability fallback still runs the instrumentation shell so
		// the event is emitted, but it must not admit the irreversible user body.
		if !execMeta.suppressUserTestBody {
			testInfo.originalFunc(t)
		}
		bodyReturned = true
	}

	// Register the instrumented func as an internal instrumented func (to avoid double instrumentation)
	setInstrumentationMetadata(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()), &instrumentationMetadata{IsInternal: true})

	// Get the additional feature wrapper
	return applyAdditionalFeaturesToTestFunc(instrumentedFunc, &testInfo.commonInfo, nil, wrapperOpts)
}

func shouldCollectExecutionCoverage(coverageEnabled bool, execMeta *testExecutionMetadata) bool {
	return coverageEnabled && (execMeta == nil || !execMeta.isARetry && !execMeta.suppressCoverageCollection)
}

// instrumentInternalBenchmarks instruments the internal benchmarks for CI visibility.
func (ddm *M) instrumentInternalBenchmarks(internalBenchmarks *[]testing.InternalBenchmark, claim *testingMInstrumentationClaim) {
	if internalBenchmarks == nil {
		return
	}
	if claim != nil {
		claim.benchmarkDescriptors = internalBenchmarks
	}

	// Extract info from internal benchmarks
	benchmarkInfos = make([]*testingBInfo, len(*internalBenchmarks))
	for idx, benchmark := range *internalBenchmarks {
		moduleName, suiteName := utils.GetModuleAndSuiteName(reflect.Indirect(reflect.ValueOf(benchmark.F)).Pointer())
		identity := newTestIdentity(moduleName, suiteName, benchmark.Name)
		benchmarkInfo := &testingBInfo{
			originalFunc: benchmark.F,
			commonInfo: commonInfo{
				moduleName: moduleName,
				suiteName:  suiteName,
				testName:   benchmark.Name,
				identity:   identity,
			},
		}

		// Increment the test count in the module.
		addModulesCounters(moduleName, 1)

		// Increment the test count in the suite.
		addSuitesCounters(suiteName, 1)

		benchmarkInfos[idx] = benchmarkInfo
	}

	// Create a new instrumented internal benchmarks
	newBenchmarkArray := make([]testing.InternalBenchmark, len(*internalBenchmarks))
	for idx, benchmarkInfo := range benchmarkInfos {
		instrumented := ddm.executeInternalBenchmark(benchmarkInfo)
		newBenchmarkArray[idx] = testing.InternalBenchmark{
			Name: benchmarkInfo.testName,
			F:    instrumented,
		}
		if claim != nil {
			if claim.benchmarks == nil {
				claim.benchmarks = make(map[string]func(*testing.B), len(benchmarkInfos))
			}
			claim.benchmarks[benchmarkInfo.testName] = benchmarkInfo.originalFunc
		}
	}

	*internalBenchmarks = newBenchmarkArray
}

func restoreTestingMWorkloads(m *testing.M, claim *testingMInstrumentationClaim) {
	if m == nil || claim == nil {
		return
	}
	restoreTestingMTests(claim.testDescriptors, claim.tests)
	restoreTestingMBenchmarks(claim.benchmarkDescriptors, claim.benchmarks)
}

func retireTestingMInstrumentation(m *testing.M, claim *testingMInstrumentationClaim) {
	if claim == nil {
		return
	}
	testingMInstrumentationClaims.mu.Lock()
	defer testingMInstrumentationClaims.mu.Unlock()
	if m != nil && testingMInstrumentationClaims.claimed[m] != claim {
		return
	}
	if claim.retired {
		return
	}
	restoreTestingMWorkloads(m, claim)
	claim.retired = true
}

func restoreTestingMTests(tests *[]testing.InternalTest, originals map[string]func(*testing.T)) {
	if tests == nil || len(originals) == 0 {
		return
	}
	for idx := range *tests {
		if original, ok := originals[(*tests)[idx].Name]; ok {
			(*tests)[idx].F = original
		}
	}
}

func restoreTestingMBenchmarks(benchmarks *[]testing.InternalBenchmark, originals map[string]func(*testing.B)) {
	if benchmarks == nil || len(originals) == 0 {
		return
	}
	for idx := range *benchmarks {
		if original, ok := originals[(*benchmarks)[idx].Name]; ok {
			(*benchmarks)[idx].F = original
		}
	}
}

// executeInternalBenchmark wraps the original benchmark function to include CI visibility instrumentation.
func (ddm *M) executeInternalBenchmark(benchmarkInfo *testingBInfo) func(*testing.B) {
	originalFunc := runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(benchmarkInfo.originalFunc)).Pointer())

	settings := integrations.GetSettings()
	testIsNew := true

	// Check if the test is known
	if settings.KnownTestsEnabled {
		testIsKnown, testKnownDataOk := isKnownTest(&benchmarkInfo.commonInfo)
		testIsNew = testKnownDataOk && !testIsKnown
	} else {
		// We don't mark any test as new if the feature is disabled
		testIsNew = false
	}

	instrumentedInternalFunc := func(b *testing.B) {

		// decrement level
		pBench := getBenchmarkPrivateFields(b)
		if pBench != nil {
			pBench.AddLevel(-1)
		}

		startTime := time.Now()
		module := session.GetOrCreateModule(benchmarkInfo.moduleName, integrations.WithTestModuleStartTime(startTime))
		suite := module.GetOrCreateSuite(benchmarkInfo.suiteName, integrations.WithTestSuiteStartTime(startTime))
		test := suite.CreateTest(benchmarkInfo.testName, integrations.WithTestStartTime(startTime))
		test.SetTestFunc(originalFunc)

		// If the execution is for a new test we tag the test event as new
		if testIsNew {
			// Set the is new test tag
			test.SetTag(constants.TestIsNew, "true")
		}

		// Run the original benchmark function.
		var iPfOfB *benchmarkPrivateFields
		var recoverFunc *func(r any)
		instrumentedFunc := func(b *testing.B) {
			// Stop the timer to perform initialization and replacements.
			b.StopTimer()

			defer func() {
				if r := recover(); r != nil {
					// Handle panic if it occurs during benchmark execution.
					if recoverFunc != nil {
						fn := *recoverFunc
						fn(r)
					}
					panic(r)
				}
			}()

			// Enable allocation reporting.
			b.ReportAllocs()

			// Retrieve the private fields of the inner testing.B.
			iPfOfB = getBenchmarkPrivateFields(b)
			if iPfOfB == nil {
				panic("failed to get private fields of the inner testing.B")
			}

			// Replace the benchmark function with the original one (this must be executed only once - the first iteration[b.run1]).
			if iPfOfB.benchFunc == nil {
				panic("failed to get the original benchmark function")
			}
			*iPfOfB.benchFunc = benchmarkInfo.originalFunc

			// Get the metadata regarding the execution (in case is already created from the additional features)
			execMeta := getTestMetadata(b)
			if execMeta == nil {
				// in case there's no additional features then we create the metadata for this execution and defer the disposal
				execMeta = createTestMetadata(b, nil)
				defer deleteTestMetadata(b)
			}

			// Sets the CI Visibility test
			execMeta.test = test

			// Restart the timer and execute the original benchmark function.
			b.ResetTimer()
			b.StartTimer()
			benchmarkInfo.originalFunc(b)
		}

		setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedFunc)).Pointer()))
		b.Run(b.Name(), instrumentedFunc)

		endTime := time.Now()
		results := iPfOfB.result

		// Set benchmark data for CI visibility.
		test.SetBenchmarkData("duration", map[string]any{
			"run":  results.N,
			"mean": results.NsPerOp(),
		})
		test.SetBenchmarkData("memory_total_operations", map[string]any{
			"run":            results.N,
			"mean":           results.AllocsPerOp(),
			"statistics.max": results.MemAllocs,
		})
		test.SetBenchmarkData("mean_heap_allocations", map[string]any{
			"run":  results.N,
			"mean": results.AllocedBytesPerOp(),
		})
		test.SetBenchmarkData("total_heap_allocations", map[string]any{
			"run":  results.N,
			"mean": iPfOfB.result.MemBytes,
		})
		if len(results.Extra) > 0 {
			mapConverted := map[string]any{}
			for k, v := range results.Extra {
				mapConverted[k] = v
			}
			test.SetBenchmarkData("extra", mapConverted)
		}

		// Define a function to handle panic during benchmark finalization.
		panicFunc := func(r any) {
			test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(r), utils.GetStacktrace(1)))
			suite.SetTag(ext.Error, true)
			module.SetTag(ext.Error, true)
			test.Close(integrations.ResultStatusFail)
			checkModuleAndSuite(module, suite)
			integrations.ExitCiVisibility()
		}
		recoverFunc = &panicFunc

		// Normal finalization: determine the benchmark result based on its state.
		if iPfOfB.B.Failed() {
			test.SetTag(ext.Error, true)
			suite.SetTag(ext.Error, true)
			module.SetTag(ext.Error, true)
			test.Close(integrations.ResultStatusFail, integrations.WithTestFinishTime(endTime))
		} else if iPfOfB.B.Skipped() {
			test.Close(integrations.ResultStatusSkip, integrations.WithTestFinishTime(endTime))
		} else {
			test.Close(integrations.ResultStatusPass, integrations.WithTestFinishTime(endTime))
		}

		checkModuleAndSuite(module, suite)
	}
	setCiVisibilityBenchmarkFunc(originalFunc)
	setCiVisibilityBenchmarkFunc(runtime.FuncForPC(reflect.Indirect(reflect.ValueOf(instrumentedInternalFunc)).Pointer()))
	return instrumentedInternalFunc
}

// RunM runs the tests and benchmarks using CI visibility.
func RunM(m *testing.M) int {
	return (*M)(m).Run()
}

// checkModuleAndSuite checks and closes the modules and suites if all tests are executed.
func checkModuleAndSuite(module integrations.TestModule, suite integrations.TestSuite) {
	// If all tests in a suite has been executed we can close the suite
	if addSuitesCounters(suite.Name(), -1) <= 0 {
		suite.Close()
	}

	// If all tests in a module has been executed we can close the module
	if addModulesCounters(module.Name(), -1) <= 0 {
		module.Close()
	}
}

// addSuitesCounters increments the suite counters for a given suite name.
func addSuitesCounters(suiteName string, delta int) int {
	suitesCountersMutex.Lock()
	defer suitesCountersMutex.Unlock()
	nValue := suitesCounters[suiteName] + delta
	suitesCounters[suiteName] = nValue
	return nValue
}

// addModulesCounters increments the module counters for a given module name.
func addModulesCounters(moduleName string, delta int) int {
	modulesCountersMutex.Lock()
	defer modulesCountersMutex.Unlock()
	nValue := modulesCounters[moduleName] + delta
	modulesCounters[moduleName] = nValue
	return nValue
}

// isKnownTest checks if a test is a known test or a new one
func isKnownTest(testInfo *commonInfo) (isKnown bool, hasKnownData bool) {
	knownTestsData := integrations.GetKnownTests()
	if knownTestsData != nil && len(knownTestsData.Tests) > 0 {
		// Check if the test is a known test or a new one
		if knownSuites, ok := knownTestsData.Tests[testInfo.moduleName]; ok {
			if knownTests, ok := knownSuites[testInfo.suiteName]; ok {
				return slices.Contains(knownTests, testInfo.testName), true
			}
		}

		return false, true
	}

	return false, false
}

// getTestManagementData retrieves the test management data for a test identity.
// It returns the matched properties, the type of match, and a flag indicating whether
// test-management data exists for the containing module/suite.
func getTestManagementData(identity *testIdentity) (data *net.TestManagementTestsResponseDataTestPropertiesAttributes, matchKind testManagementMatchKind, hasTestManagementData bool) {
	testManagementData := integrations.GetTestManagementTestsData()
	return matchTestManagementData(identity, testManagementData)
}

// matchTestManagementData finds the best-matching test-management directive for a given identity within the provided dataset.
func matchTestManagementData(identity *testIdentity, modules *net.TestManagementTestsResponseDataModules) (data *net.TestManagementTestsResponseDataTestPropertiesAttributes, matchKind testManagementMatchKind, hasTestManagementData bool) {
	if identity == nil || modules == nil || len(modules.Modules) == 0 {
		return nil, testManagementMatchNone, false
	}

	module, ok := modules.Modules[identity.ModuleName]
	if !ok {
		return nil, testManagementMatchNone, true
	}

	suite, ok := module.Suites[identity.SuiteName]
	if !ok {
		return nil, testManagementMatchNone, true
	}

	if len(suite.Tests) == 0 {
		return nil, testManagementMatchNone, true
	}

	for i := len(identity.Segments); i > 0; i-- {
		candidate := strings.Join(identity.Segments[:i], "/")
		if test, ok := suite.Tests[candidate]; ok {
			kind := testManagementMatchExact
			if candidate != identity.FullName {
				kind = testManagementMatchAncestor
			}
			return &test.Properties, kind, true
		}
	}

	return nil, testManagementMatchNone, true
}

// setTestTagsFromExecutionMetadata sets the test tags from the execution metadata.
func setTestTagsFromExecutionMetadata(test integrations.Test, execMeta *testExecutionMetadata) (cancelExecution bool) {
	cancelExecution = setTestTagsFromExecutionMetadataNoClose(test, execMeta)
	if cancelExecution {
		test.Close(integrations.ResultStatusSkip, integrations.WithTestSkipReason(constants.TestDisabledSkipReason))
	}
	return cancelExecution
}

func setTestTagsFromExecutionMetadataNoClose(test integrations.Test, execMeta *testExecutionMetadata) (cancelExecution bool) {
	settings := integrations.GetSettings()

	// Set the Test Optimization test to the execution metadata
	execMeta.test = test
	if execMeta.identity != nil && len(execMeta.identity.Segments) > 1 {
		log.Debug("setTestTagsFromExecutionMetadata assigned test for %s", execMeta.identity.FullName)
	}

	// If the execution is for a new test we tag the test event as new
	if execMeta.isANewTest {
		// Set the is new test tag
		test.SetTag(constants.TestIsNew, "true")
	}

	// If the execution is for a modified test
	execMeta.isAModifiedTest = execMeta.isAModifiedTest || (settings.ImpactedTestsEnabled && test.Context().Value(constants.TestIsModified) == true)

	// If the execution is a retry we tag the test event
	if execMeta.isARetry {
		// Set the retry tag
		test.SetTag(constants.TestIsRetry, "true")

		// let's set the retry reason
		if execMeta.isAttemptToFix {
			// Set attempt_to_fix as the retry reason
			test.SetTag(constants.TestRetryReason, constants.AttemptToFixRetryReason)
		} else if usesEfdRetrySemantics(execMeta) {
			// Set early_flake_detection as the retry reason
			test.SetTag(constants.TestRetryReason, constants.EarlyFlakeDetectionRetryReason)
		} else if execMeta.isFlakyTestRetriesEnabled {
			// Set auto_test_retry as the retry reason
			test.SetTag(constants.TestRetryReason, constants.AutoTestRetriesRetryReason)
		} else {
			// Set the unknown reason
			test.SetTag(constants.TestRetryReason, constants.ExternalRetryReason)
		}
	}

	// If the test is an attempt to fix we tag the test event
	if execMeta.isAttemptToFix {
		test.SetTag(constants.TestIsAttempToFix, "true")
	}

	// If the test is quarantined we tag the test event
	if execMeta.isQuarantined {
		test.SetTag(constants.TestIsQuarantined, "true")
	}

	// If the test is disabled we tag the test event
	if execMeta.isDisabled {
		test.SetTag(constants.TestIsDisabled, "true")
		if !execMeta.isAttemptToFix {
			// Disabled test without ATF is always a final execution, set the final status
			test.SetTag(constants.TestFinalStatus, constants.TestStatusSkip)
			return true
		}
	}

	return false
}

// instrumentChattyPrinter initializes the chatty printer for verbose output if logging is enabled.
func instrumentChattyPrinter(t *testing.T) {
	if !logs.IsEnabled() {
		// If the logs integration is not enabled, we don't need to instrument the chatty printer.
		return
	}

	// Initialize the chatty printer if not already done.
	chattyPrinterOnce.Do(func() {
		chatty = getTestChattyPrinter(t)
		// If the chatty printer is enabled, we wrap the writer to capture output.
		if chatty != nil && chatty.w != nil && *chatty.w != nil {
			*chatty.w = &customWriter{chatty: chatty, writer: *chatty.w}
		}
	})
}

// collectAndWriteLogs collects logs from the chatty printer and the test output, and writes them to the test.
func collectAndWriteLogs(t *testing.T, test integrations.Test, attemptOutput []byte) {
	// Ensure any buffered partial line (Go 1.25+) is flushed before extracting the test output.
	// This is a no-op on Go versions that don't have the output writer partial buffer.
	flushOutputWriterPartial(t)

	if !logs.IsEnabled() {
		// If the logs integration is not enabled, we don't need to collect or write logs.
		return
	}

	if chatty != nil && chatty.w != nil && *chatty.w != nil {
		if writer, ok := (*chatty.w).(*customWriter); ok {
			strOutput := writer.GetOutput(test.Name())
			if len(strOutput) > 0 {
				sc := bufio.NewScanner(strings.NewReader(strOutput))
				for sc.Scan() {
					test.Log(sc.Text(), "")
				}

				// if the chatty printer has output, we skip the test output extraction
				return
			}
		}
	}

	if attemptOutput == nil {
		if tCommon := getTestPrivateFields(t); tCommon != nil && tCommon.output != nil {
			attemptOutput = tCommon.GetOutput()
		}
	}
	if len(attemptOutput) > 0 {
		sc := bufio.NewScanner(strings.NewReader(string(attemptOutput)))
		for sc.Scan() {
			test.Log(sc.Text(), "")
		}
	}
}
