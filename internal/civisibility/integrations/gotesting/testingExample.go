// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bufio"
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

		stdout := os.Stdout
		reader, writer, err := os.Pipe()
		if err != nil {
			message := fmt.Sprintf("capture example output: %v", err)
			finishExampleEvent(test, suite, module, time.Now(), nil, message, nil, "", false)
			panic(message)
		}
		os.Stdout = writer
		output := make(chan string, 1)
		go func() {
			var captured strings.Builder
			_, copyErr := io.Copy(&captured, reader)
			_ = reader.Close()
			if copyErr != nil {
				fmt.Fprintf(os.Stderr, "civisibility: copying example output: %v\n", copyErr)
			}
			output <- captured.String()
		}()

		finished := false
		defer func() {
			finishTime := time.Now()
			_ = writer.Close()
			os.Stdout = stdout
			captured := <-output
			_, _ = io.WriteString(stdout, captured)

			panicData := recover()
			mismatch := exampleOutputMismatch(captured, info.output, info.unordered)
			stack := ""
			if panicData != nil || !finished {
				stack = utils.GetStacktrace(1)
			}
			finishExampleEvent(test, suite, module, finishTime, []byte(captured), mismatch, panicData, stack, !finished)
			if panicData != nil {
				panic(panicData)
			}
		}()

		info.originalFunc()
		finished = true
	}
}

// finishExampleEvent records captured output and closes the native test event
// with the example's output, panic, or runtime.Goexit result.
func finishExampleEvent(
	test integrations.Test,
	suite integrations.TestSuite,
	module integrations.TestModule,
	finishTime time.Time,
	output []byte,
	mismatch string,
	panicData any,
	stack string,
	unfinished bool,
) {
	if len(output) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			test.Log(scanner.Text(), "")
		}
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
		test.SetTag(constants.TestFinalStatus, constants.TestStatusFail)
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
		test.Close(integrations.ResultStatusFail, integrations.WithTestFinishTime(finishTime))
	} else {
		test.SetTag(constants.TestFinalStatus, constants.TestStatusPass)
		test.Close(integrations.ResultStatusPass, integrations.WithTestFinishTime(finishTime))
	}
	checkModuleAndSuite(module, suite)
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
		return fmt.Sprintf("got:\n%s\nwant:\n%s\n", got, want)
	}
	return ""
}
