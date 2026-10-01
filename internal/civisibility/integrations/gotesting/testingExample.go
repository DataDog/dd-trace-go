// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

type testingExampleInfo struct {
	commonInfo
	originalFunc func()
	output       string
	unordered    bool
}

type exampleExecutionResult struct {
	finished  bool
	panicData any
	stack     string
}

type exampleOutputCaptureResult struct {
	output string
	err    error
}

// instrumentInternalExamples replaces testing's example descriptors with CI
// Visibility wrappers and retains the originals in claim for later restoration.
func (ddm *M) instrumentInternalExamples(examples *[]testing.InternalExample, claim *testingMInstrumentationClaim) {
	if examples == nil || !exampleOutputCaptureSupported() {
		return
	}
	if claim != nil {
		claim.exampleDescriptors = examples
		claim.examples = make(map[string]func(), len(*examples))
	}

	wrapped := make([]testing.InternalExample, len(*examples))
	for idx, example := range *examples {
		fn := runtime.FuncForPC(reflect.ValueOf(example.F).Pointer())
		moduleName, suiteName := utils.GetModuleAndSuiteName(fn.Entry())
		addModulesCounters(moduleName, 1)
		addSuitesCounters(suiteName, 1)
		info := &testingExampleInfo{
			originalFunc: example.F,
			output:       example.Output,
			unordered:    example.Unordered,
			commonInfo: commonInfo{
				moduleName: moduleName,
				suiteName:  suiteName,
				testName:   example.Name,
				identity:   newTestIdentity(moduleName, suiteName, example.Name),
				sourceFunc: fn,
			},
		}
		wrapped[idx] = testing.InternalExample{
			Name:      example.Name,
			F:         ddm.executeInternalExample(info),
			Output:    example.Output,
			Unordered: example.Unordered,
		}
		if claim != nil {
			claim.examples[example.Name] = example.F
		}
	}
	*examples = wrapped
}

// exampleOutputCaptureSupported reports whether examples can be wrapped
// in-process. It follows testing's use of a separate runner where os.Pipe is
// unavailable.
func exampleOutputCaptureSupported() bool {
	return runtime.GOOS != "js" && runtime.GOOS != "wasip1"
}

// executeInternalExample captures and replays stdout while reporting the same
// output comparison and panic outcome that testing assigns to the example.
func (ddm *M) executeInternalExample(info *testingExampleInfo) func() {
	return func() {
		startTime := time.Now()
		module := session.GetOrCreateModule(info.moduleName, integrations.WithTestModuleStartTime(startTime))
		suite := module.GetOrCreateSuite(info.suiteName, integrations.WithTestSuiteStartTime(startTime))
		test := suite.CreateTest(info.testName, integrations.WithTestStartTime(startTime))
		test.SetTestFunc(info.sourceFunc)
		execMeta := &testExecutionMetadata{identity: info.identity}
		if meta := testManagementOnlyMetadata(info.identity); meta != nil {
			applyAdditionalFeatureMetadataToExecution(execMeta, meta)
		}
		if setTestTagsFromExecutionMetadata(test, execMeta) {
			checkModuleAndSuite(module, suite)
			// Examples have no testing.T to skip. Replaying the declared output
			// keeps the outer testing example runner green without executing the
			// disabled body.
			_, _ = io.WriteString(os.Stdout, info.output)
			return
		}
		maskedByTestManagement := execMeta.isDisabled || execMeta.isQuarantined

		stdout := os.Stdout
		reader, writer, err := os.Pipe()
		if err != nil {
			message := fmt.Sprintf("capture example output: %v", err)
			finishExampleEvent(test, suite, module, execMeta, time.Now(), nil, message, nil, "", false)
			panic(message)
		}
		os.Stdout = writer
		output := make(chan exampleOutputCaptureResult, 1)
		go func() {
			captured := captureExampleOutput(reader)
			_ = reader.Close()
			output <- captured
		}()

		finished := false
		var isolatedResult *exampleExecutionResult
		defer func() {
			finishTime := time.Now()
			_ = writer.Close()
			os.Stdout = stdout
			capture := <-output
			captured := capture.output

			panicData := recover()
			stack := ""
			if isolatedResult != nil {
				finished = isolatedResult.finished
				panicData = isolatedResult.panicData
				stack = isolatedResult.stack
			}
			if capture.err != nil {
				panicData = fmt.Errorf("copying example output: %w", capture.err)
				stack = utils.GetStacktrace(1)
				finished = false
			}
			replayed := captured
			if maskedByTestManagement && isolatedResult != nil {
				// The native event keeps the real output and outcome. The standard
				// example runner receives the declared output so quarantine does
				// not fail the package.
				replayed = info.output
			}
			_, _ = io.WriteString(stdout, replayed)

			mismatch := exampleOutputMismatch(captured, info.output, info.unordered)
			if stack == "" && (panicData != nil || !finished) {
				stack = utils.GetStacktrace(1)
			}
			finishExampleEvent(test, suite, module, execMeta, finishTime, []byte(captured), mismatch, panicData, stack, !finished)
			if maskedByTestManagement && isolatedResult != nil && capture.err == nil {
				return
			}
			if panicData != nil {
				panic(panicData)
			}
			if !finished {
				// recover returns nil for panic(nil) when GODEBUG=panicnil=1 and
				// for runtime.Goexit. Preserve testing's terminal behavior in both cases.
				panic(errTestingDidNotReturn)
			}
		}()

		if maskedByTestManagement {
			result := runManagedExample(info.originalFunc)
			isolatedResult = &result
			finished = result.finished
			return
		}
		info.originalFunc()
		finished = true
	}
}

func captureExampleOutput(reader io.Reader) exampleOutputCaptureResult {
	var captured strings.Builder
	_, err := io.Copy(&captured, reader)
	return exampleOutputCaptureResult{output: captured.String(), err: err}
}

// runManagedExample isolates the example body so runtime.Goexit can be
// observed and masked for quarantined workloads without terminating the outer
// example runner's goroutine.
func runManagedExample(fn func()) (result exampleExecutionResult) {
	completed := make(chan exampleExecutionResult, 1)
	go func() {
		var local exampleExecutionResult
		defer func() {
			local.panicData = recover()
			if local.panicData != nil || !local.finished {
				local.stack = utils.GetStacktrace(1)
			}
			completed <- local
		}()
		fn()
		local.finished = true
	}()
	return <-completed
}

// finishExampleEvent records captured output and closes the native test event
// with the example's output, panic, or runtime.Goexit result.
func finishExampleEvent(
	test integrations.Test,
	suite integrations.TestSuite,
	module integrations.TestModule,
	execMeta *testExecutionMetadata,
	finishTime time.Time,
	output []byte,
	mismatch string,
	panicData any,
	stack string,
	unfinished bool,
) {
	if len(output) > 0 {
		forEachExampleOutputLine(output, func(line string) { test.Log(line, "") })
	}

	switch {
	case panicData != nil:
		test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(panicData), stack))
	case unfinished:
		test.SetError(integrations.WithErrorInfo("runtime.Goexit", "example did not return", stack))
	case mismatch != "":
		test.SetError(integrations.WithErrorInfo("output", mismatch, ""))
	}
	if panicData != nil || unfinished || mismatch != "" {
		test.SetTag(constants.TestFinalStatus, calculateFinalStatus(false, true, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
		test.Close(integrations.ResultStatusFail, integrations.WithTestFinishTime(finishTime))
	} else {
		test.SetTag(constants.TestFinalStatus, calculateFinalStatus(true, false, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
		test.Close(integrations.ResultStatusPass, integrations.WithTestFinishTime(finishTime))
	}
	checkModuleAndSuite(module, suite)
}

// forEachExampleOutputLine applies bufio.ScanLines semantics without its
// 64 KiB token limit, since examples may legitimately print larger lines.
func forEachExampleOutputLine(output []byte, visit func(string)) {
	for len(output) > 0 {
		line, rest, found := bytes.Cut(output, []byte{'\n'})
		if !found {
			visit(string(bytes.TrimSuffix(output, []byte{'\r'})))
			return
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		visit(string(line))
		output = rest
	}
}

// exampleOutputMismatch mirrors testing's ordered and unordered output
// comparison rules and returns an empty string when the output matches.
func exampleOutputMismatch(gotOutput, wantOutput string, unordered bool) string {
	got := strings.TrimSpace(gotOutput)
	want := strings.TrimSpace(wantOutput)
	if runtime.GOOS == "windows" {
		got = strings.ReplaceAll(got, "\r\n", "\n")
		want = strings.ReplaceAll(want, "\r\n", "\n")
	}
	if unordered {
		gotLines := strings.Split(got, "\n")
		wantLines := strings.Split(want, "\n")
		slices.Sort(gotLines)
		slices.Sort(wantLines)
		if !slices.Equal(gotLines, wantLines) {
			return fmt.Sprintf("got:\n%s\nwant (unordered):\n%s\n", gotOutput, wantOutput)
		}
		return ""
	}
	if got != want {
		return fmt.Sprintf("got:\n%s\nwant:\n%s\n", gotOutput, wantOutput)
	}
	return ""
}
