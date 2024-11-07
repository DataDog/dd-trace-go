// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)

// Test

// Ensures that tslvTest implements the Test interface.
var _ Test = (*tslvTest)(nil)

// tslvTest implements the DdTest interface and represents an individual test within a suite.
type tslvTest struct {
	ciVisibilityCommon
	testID uint64
	suite  *tslvTestSuite
	name   string
}

// createTest initializes a new test within a given suite.
func createTest(suite *tslvTestSuite, name string, startTime time.Time) Test {
	if suite == nil {
		return nil
	}

	operationName := "test"
	if suite.module.framework != "" {
		operationName = fmt.Sprintf("%s.%s", strings.ToLower(suite.module.framework), operationName)
	}

	resourceName := fmt.Sprintf("%s.%s", suite.name, name)

	// Test tags should include suite, module, and session tags so the backend can calculate the suite, module, and session fingerprint from the test.
	testTags := append(suite.tags, tracer.Tag(constants.TestName, name))
	testOpts := append(fillCommonTags([]tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(constants.SpanTypeTest),
		tracer.StartTime(startTime),
	}), testTags...)

	span, ctx := tracer.StartSpanFromContext(context.Background(), operationName, testOpts...)
	if suite.module.session != nil {
		span.SetTag(constants.TestSessionIDTag, fmt.Sprint(suite.module.session.sessionID))
	}
	span.SetTag(constants.TestModuleIDTag, fmt.Sprint(suite.module.moduleID))
	span.SetTag(constants.TestSuiteIDTag, fmt.Sprint(suite.suiteID))
	testID := span.Context().SpanID()

	t := &tslvTest{
		testID: testID,
		suite:  suite,
		name:   name,
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: startTime,
			tags:      testTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Note: if the process is killed some tests will not be closed and will be lost. This is a known limitation.
	// We will not close it because there's no a good test status to report in this case, and we don't want to report a false positive (pass, fail, or skip).

	// Creating telemetry event created
	telemetry.EventCreated(t.suite.module.framework, telemetry.TestEventType)
	return t
}

// TestID returns the ID of the test.
func (t *tslvTest) TestID() uint64 {
	return t.testID
}

// Name returns the name of the test.
func (t *tslvTest) Name() string { return t.name }

// Suite returns the suite to which the test belongs.
func (t *tslvTest) Suite() TestSuite { return t.suite }

// Close closes the test with the given status.
func (t *tslvTest) Close(status TestResultStatus, options ...TestCloseOption) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}

	defaults := &tslvTestCloseOptions{}
	for _, opt := range options {
		opt(defaults)
	}

	if defaults.finishTime.IsZero() {
		defaults.finishTime = time.Now()
	}

	switch status {
	case ResultStatusPass:
		t.span.SetTag(constants.TestStatus, constants.TestStatusPass)
	case ResultStatusFail:
		t.span.SetTag(constants.TestStatus, constants.TestStatusFail)
	case ResultStatusSkip:
		t.span.SetTag(constants.TestStatus, constants.TestStatusSkip)
	}

	if defaults.skipReason != "" {
		t.span.SetTag(constants.TestSkipReason, defaults.skipReason)
	}

	t.span.Finish(tracer.FinishTime(defaults.finishTime))
	t.closed = true

	// Creating telemetry event finished
	testingEventType := telemetry.TestEventType
	if t.ctx.Value(constants.TestIsNew) == "true" {
		testingEventType = append(testingEventType, telemetry.IsNewEventType...)
	}
	if t.ctx.Value(constants.TestIsRetry) == "true" {
		testingEventType = append(testingEventType, telemetry.IsRetryEventType...)
	}
	if t.ctx.Value(constants.TestEarlyFlakeDetectionRetryAborted) == "slow" {
		testingEventType = append(testingEventType, telemetry.EfdAbortSlowEventType...)
	}
	if t.ctx.Value(constants.TestType) == constants.TestTypeBenchmark {
		testingEventType = append(testingEventType, telemetry.IsBenchmarkEventType...)
	}
	telemetry.EventFinished(t.suite.module.framework, testingEventType)
}

// SetTag sets a tag on the test event.
func (t *tslvTest) SetTag(key string, value interface{}) {
	t.ciVisibilityCommon.SetTag(key, value)
	if key == constants.TestIsNew {
		t.ctx = context.WithValue(t.ctx, constants.TestIsNew, value)
	} else if key == constants.TestIsRetry {
		t.ctx = context.WithValue(t.ctx, constants.TestIsRetry, value)
	} else if key == constants.TestEarlyFlakeDetectionRetryAborted {
		t.ctx = context.WithValue(t.ctx, constants.TestEarlyFlakeDetectionRetryAborted, value)
	}
}

// SetError sets an error on the test and marks the suite and module as having an error.
func (t *tslvTest) SetError(options ...ErrorOption) {
	t.ciVisibilityCommon.SetError(options...)
	t.Suite().SetTag(ext.Error, true)
	t.Suite().Module().SetTag(ext.Error, true)
}

// SetTestFunc sets the function to be tested and records its source location.
func (t *tslvTest) SetTestFunc(fn *runtime.Func) {
	if fn == nil {
		return
	}

	// let's get the file path and the start line of the function
	absolutePath, startLine := fn.FileLine(fn.Entry())
	file := utils.GetRelativePathFromCITagsSourceRoot(absolutePath)
	t.SetTag(constants.TestSourceFile, file)
	t.SetTag(constants.TestSourceStartLine, startLine)

	// now, let's try to get the end line of the function using ast
	// parse the entire file where the function is defined to create an abstract syntax tree (AST)
	// if we can't parse the file (source code is not available) we silently bail out
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absolutePath, nil, parser.AllErrors|parser.ParseComments)
	if err == nil {

		// let's check if the suite was marked as unskippable before
		isUnskippable, hasUnskippableValue := t.suite.ctx.Value(constants.TestUnskippable).(bool)
		if !hasUnskippableValue {
			// check for suite level unskippable comment at the top of the file
			for _, commentGroup := range fileNode.Comments {
				for _, comment := range commentGroup.List {
					if strings.Contains(comment.Text, "//dd:suite.unskippable") {
						isUnskippable = true
						break
					}
				}
				if isUnskippable {
					break
				}
			}
			t.suite.ctx = context.WithValue(t.suite.ctx, constants.TestUnskippable, isUnskippable)
		}

		// get the function name without the package name
		fullName := fn.Name()
		firstDot := strings.LastIndex(fullName, ".") + 1
		name := fullName[firstDot:]

		// variable to store the ending line of the function
		var endLine int

		// traverse the AST to find the function declaration for the target function
		ast.Inspect(fileNode, func(n ast.Node) bool {
			// check if the current node is a function declaration
			if funcDecl, ok := n.(*ast.FuncDecl); ok {
				// if the function name matches the target function name
				if funcDecl.Name.Name == name {
					// get the line number of the end of the function body
					endLine = fset.Position(funcDecl.Body.End()).Line
					// check for comments above the function declaration to look for unskippable tag
					// but only if we haven't found a suite level unskippable comment
					if !isUnskippable && funcDecl.Doc != nil {
						for _, comment := range funcDecl.Doc.List {
							if strings.Contains(comment.Text, "//dd:test.unskippable") {
								isUnskippable = true
								break
							}
						}
					}

					// stop further inspection since we have found the target function
					return false
				}
			}
			// check if the current node is a function literal (FuncLit)
			if funcLit, ok := n.(*ast.FuncLit); ok {
				// get the line number of the start of the function literal
				funcStartLine := fset.Position(funcLit.Body.Pos()).Line
				// if the start line matches the known start line, record the end line
				if funcStartLine == startLine {
					endLine = fset.Position(funcLit.Body.End()).Line
					return false // stop further inspection since we have found the function
				}
			}
			// continue inspecting other nodes
			return true
		})

		// if we found an endLine we check is greater than the calculated startLine
		if endLine >= startLine {
			t.SetTag(constants.TestSourceEndLine, endLine)
		}

		// if the function is marked as unskippable, set the appropriate tag
		if isUnskippable {
			t.SetTag(constants.TestUnskippable, "true")
			telemetry.ITRUnskippable(telemetry.TestEventType)
			t.ctx = context.WithValue(t.ctx, constants.TestUnskippable, true)
		}
	}

	// get the codeowner of the function
	codeOwners := utils.GetCodeOwners()
	if codeOwners != nil {
		match, found := codeOwners.Match("/" + file)
		if found {
			t.SetTag(constants.TestCodeOwners, match.GetOwnersString())
		}
	}
}

// SetBenchmarkData sets benchmark data for the test.
func (t *tslvTest) SetBenchmarkData(measureType string, data map[string]any) {
	t.span.SetTag(constants.TestType, constants.TestTypeBenchmark)
	t.ctx = context.WithValue(t.ctx, constants.TestType, constants.TestTypeBenchmark)
	for k, v := range data {
		t.span.SetTag(fmt.Sprintf("benchmark.%s.%s", measureType, k), v)
	}
}
