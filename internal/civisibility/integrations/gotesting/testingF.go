// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

// F adapts testing.F methods that need CI Visibility instrumentation.
type F testing.F

// GetFuzz returns the CI Visibility adapter for f.
func GetFuzz(f *testing.F) *F {
	return (*F)(f)
}

// Fuzz runs ff as the fuzz target and reports each seed corpus execution as a
// native test event. Active fuzzing mutations remain owned by testing.F.
func (ddf *F) Fuzz(ff any) {
	f := (*testing.F)(ddf)
	if isTestingBuiltWithOrchestrion() {
		// The woven testing.F.Fuzz body instruments this argument. Avoid wrapping
		// it twice when callers use the manual adapter in an Orchestrion binary.
		f.Fuzz(ff)
		return
	}
	f.Fuzz(instrumentTestingFuzzFunc(ff))
}

// testingFuzzWorkerActive reports whether this process is a child worker that
// executes generated mutations on behalf of the fuzzing coordinator.
func testingFuzzWorkerActive() bool {
	if worker := flag.Lookup("test.fuzzworker"); worker != nil {
		active, err := strconv.ParseBool(worker.Value.String())
		if err == nil && active {
			return true
		}
	}
	// testing.M.Run parses test flags after its instrumentation hook executes,
	// so active fuzz workers must also be recognized from the raw command line.
	return testingFuzzWorkerRequested(os.Args[1:])
}

func testingFuzzWorkerRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			return false
		}
		var value string
		switch {
		case arg == "-test.fuzzworker" || arg == "--test.fuzzworker":
			return true
		case strings.HasPrefix(arg, "-test.fuzzworker="):
			value = strings.TrimPrefix(arg, "-test.fuzzworker=")
		case strings.HasPrefix(arg, "--test.fuzzworker="):
			value = strings.TrimPrefix(arg, "--test.fuzzworker=")
		default:
			continue
		}
		active, err := strconv.ParseBool(value)
		return err == nil && active
	}
	return false
}

type testingFInfo struct {
	commonInfo
	originalFunc func(*testing.F)
}

// instrumentInternalFuzzTargets replaces testing's fuzz descriptors with CI
// Visibility wrappers and retains the originals in claim for later restoration.
func (ddm *M) instrumentInternalFuzzTargets(targets *[]testing.InternalFuzzTarget, claim *testingMInstrumentationClaim) {
	if targets == nil {
		return
	}
	if claim != nil {
		claim.fuzzDescriptors = targets
		claim.fuzzTargets = make(map[string]func(*testing.F), len(*targets))
	}

	wrapped := make([]testing.InternalFuzzTarget, len(*targets))
	for idx, target := range *targets {
		fn := runtime.FuncForPC(reflect.ValueOf(target.Fn).Pointer())
		moduleName, suiteName := utils.GetModuleAndSuiteName(fn.Entry())
		addModulesCounters(moduleName, 1)
		addSuitesCounters(suiteName, 1)
		info := &testingFInfo{
			originalFunc: target.Fn,
			commonInfo: commonInfo{
				moduleName: moduleName,
				suiteName:  suiteName,
				testName:   target.Name,
				identity:   newTestIdentity(moduleName, suiteName, target.Name),
				sourceFunc: fn,
			},
		}
		wrapped[idx] = testing.InternalFuzzTarget{Name: target.Name, Fn: ddm.executeInternalFuzzTarget(info)}
		if claim != nil {
			claim.fuzzTargets[target.Name] = target.Fn
		}
	}
	*targets = wrapped
}

// executeInternalFuzzTarget reports the coordinator's root fuzz test. Fuzz
// workers bypass this lifecycle because generated mutations are not test cases.
func (ddm *M) executeInternalFuzzTarget(info *testingFInfo) func(*testing.F) {
	return func(f *testing.F) {
		if testingFuzzWorkerActive() {
			info.originalFunc(f)
			return
		}
		startTime := time.Now()
		module := session.GetOrCreateModule(info.moduleName, integrations.WithTestModuleStartTime(startTime))
		suite := module.GetOrCreateSuite(info.suiteName, integrations.WithTestSuiteStartTime(startTime))
		test := suite.CreateTest(info.testName, integrations.WithTestStartTime(startTime))
		test.SetTestFunc(info.sourceFunc)

		execMeta := createTestMetadata(f, nil)
		execMeta.identity = info.identity
		if meta := testManagementOnlyMetadata(info.identity); meta != nil {
			applyAdditionalFeatureMetadataToExecution(execMeta, meta)
		}
		if setTestTagsFromExecutionMetadata(test, execMeta) {
			deleteTestMetadata(f)
			checkModuleAndSuite(module, suite)
			f.Skip(constants.TestDisabledSkipReason)
			return
		}
		maskedByTestManagement := execMeta.isDisabled || execMeta.isQuarantined

		bodyReturned := false
		defer func() {
			bodyTerminal := recover()
			terminal, terminalStack := completeFuzzTargetLifecycle(f, bodyReturned, bodyTerminal)
			if terminal != nil {
				errorType := "panic"
				if terminalErr, ok := terminal.(error); ok && errors.Is(terminalErr, errTestingDidNotReturn) {
					errorType = "runtime.Goexit"
				}
				execMeta.processRetryError.CompareAndSwap(nil, &processRetryErrorInfo{
					Type:    errorType,
					Message: fmt.Sprint(terminal),
					Stack:   terminalStack,
				})
			}
			defer deleteTestMetadata(f)
			finishTestingTBEvent(f, execMeta, test, suite, module, time.Now())
			if maskedByTestManagement {
				// Keep the actual native outcome, but prevent a managed failure
				// from failing the package. Mark incomplete bodies as finished so
				// testing does not reinterpret a masked Goexit as a new panic.
				if fields := getTestPrivateFields((*testing.T)(unsafe.Pointer(f))); fields != nil {
					fields.SetFailed(false)
					fields.SetSkipped(true)
					if !bodyReturned {
						fields.SetFinished(true)
					}
				}
				return
			}
			if terminal != nil {
				panic(terminal)
			}
		}()
		info.originalFunc(f)
		bodyReturned = true
	}
}

// completeFuzzTargetLifecycle mirrors the terminal work performed by
// testing.fRunner before the root event is closed.
func completeFuzzTargetLifecycle(f *testing.F, bodyReturned bool, bodyTerminal any) (any, string) {
	cleanup := &testCleanupResult{}
	completeFuzzParallelSeeds(f)
	runTestCleanupCallbacks((*testing.T)(unsafe.Pointer(f)), cleanup)

	terminal := bodyTerminal
	terminalStack := ""
	if terminal != nil {
		terminalStack = utils.GetStacktrace(1)
		if cleanup.panicData != nil {
			f.Logf("cleanup panicked with %v", cleanup.panicData)
		}
	} else if cleanup.panicData != nil {
		terminal = cleanup.panicData
		terminalStack = cleanup.panicStacktrace
	} else if (!bodyReturned || cleanup.goexit) && !f.Failed() && !f.Skipped() {
		terminal = errTestingDidNotReturn
		terminalStack = utils.GetStacktrace(1)
	}
	if terminal != nil {
		f.Fail()
	}
	if !testingFFuzzCalled(f) && !f.Failed() && !f.Skipped() {
		f.Error("returned without calling F.Fuzz, F.Fail, or F.Skip")
	}
	return terminal, terminalStack
}

// completeFuzzParallelSeeds waits for seeds that called T.Parallel before the
// fuzz target result and its cleanups are observed.
func completeFuzzParallelSeeds(f *testing.F) {
	t := (*testing.T)(unsafe.Pointer(f))
	fields := getTestPrivateFields(t)
	state := getFuzzTestState(f)
	completeParallelSubtestsWithState(fields, state, false, false)
}

// getFuzzTestState reads testing.F's scheduler state. testing.F and testing.T
// place this field at different offsets, so the testing.T helper cannot be used.
func getFuzzTestState(f *testing.F) *testingTestState {
	ptr, err := getFieldPointerFrom(f, "tstate")
	if err != nil || ptr == nil {
		return nil
	}
	state := *(**testingTestState)(ptr)
	runtime.KeepAlive(f)
	return state
}

// testingFFuzzCalled reports whether the root invoked F.Fuzz. The standard
// library validates this after user cleanups, so the native event must do so too.
func testingFFuzzCalled(f *testing.F) bool {
	ptr, err := getFieldPointerFromWithType(f, "fuzzCalled", reflect.TypeFor[bool]())
	if err != nil || ptr == nil {
		return true
	}
	called := *(*bool)(ptr)
	runtime.KeepAlive(f)
	return called
}

// finishTestingTBEvent maps testing's terminal state to the native CI
// Visibility result and releases the enclosing suite and module workloads.
func finishTestingTBEvent(
	tb testing.TB,
	execMeta *testExecutionMetadata,
	test integrations.Test,
	suite integrations.TestSuite,
	module integrations.TestModule,
	finishTime time.Time,
) {
	switch {
	case tb.Failed():
		test.SetTag(constants.TestFinalStatus, calculateFinalStatus(false, true, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
		if captured := execMeta.processRetryError.Load(); captured != nil {
			test.SetError(integrations.WithErrorInfo(captured.Type, captured.Message, captured.Stack))
		} else {
			test.SetTag(ext.Error, true)
		}
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
		test.Close(integrations.ResultStatusFail, integrations.WithTestFinishTime(finishTime))
	case tb.Skipped():
		reason := execMeta.skipReason
		if reason == "" {
			if captured := execMeta.processRetrySkipReason.Load(); captured != nil {
				reason = *captured
			}
		}
		test.SetTag(constants.TestFinalStatus, calculateFinalStatus(false, false, true, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
		test.Close(integrations.ResultStatusSkip, integrations.WithTestFinishTime(finishTime), integrations.WithTestSkipReason(reason))
	default:
		test.SetTag(constants.TestFinalStatus, calculateFinalStatus(true, false, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
		test.Close(integrations.ResultStatusPass, integrations.WithTestFinishTime(finishTime))
	}
	checkModuleAndSuite(module, suite)
}
