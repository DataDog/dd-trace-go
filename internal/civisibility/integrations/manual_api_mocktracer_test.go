// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
)

var mockTracer mocktracer.Tracer

func TestMain(m *testing.M) {
	// Initialize civisibility using the mocktracer for testing
	mockTracer = InitializeCIVisibilityMock()

	// Avoid any backend calls during tests
	additionalFeaturesInitializationOnce = sync.Once{}
	additionalFeaturesInitializationOnce.Do(func() {})

	// Run tests
	os.Exit(m.Run())
}

func createDDTestSession(now time.Time) TestSession {
	session := CreateTestSession(WithTestSessionCommand("my-command"), WithTestSessionWorkingDirectory("/tmp/wd"), WithTestSessionFramework("my-testing-framework", "framework-version"), WithTestSessionStartTime(now))
	session.SetTag("my-tag", "my-value")
	return session
}

func createDDTestModule(now time.Time) (TestSession, TestModule) {
	session := createDDTestSession(now)
	module := session.GetOrCreateModule("my-module", WithTestModuleFramework("my-module-framework", "framework-version"), WithTestModuleStartTime(now))
	module.SetTag("my-tag", "my-value")
	return session, module
}

func createDDTestSuite(now time.Time) (TestSession, TestModule, TestSuite) {
	session, module := createDDTestModule(now)
	suite := module.GetOrCreateSuite("my-suite", WithTestSuiteStartTime(now))
	suite.SetTag("my-tag", "my-value")
	return session, module, suite
}

func createDDTest(now time.Time) (TestSession, TestModule, TestSuite, Test) {
	session, module, suite := createDDTestSuite(now)
	test := suite.CreateTest("my-test", WithTestStartTime(now))
	test.SetTag("my-tag", "my-value")
	return session, module, suite, test
}

func commonAssertions(assert *assert.Assertions, sessionSpan *mocktracer.Span) {
	tags := map[string]interface{}{
		"my-tag":              "my-value",
		constants.Origin:      constants.CIAppTestOrigin,
		constants.TestType:    constants.TestTypeTest,
		constants.TestCommand: "my-command",
	}

	spanTags := sessionSpan.Tags()

	assert.Subset(spanTags, tags)
	assert.Contains(spanTags, constants.OSPlatform)
	assert.Contains(spanTags, constants.OSArchitecture)
	assert.Contains(spanTags, constants.OSVersion)
	assert.Contains(spanTags, constants.RuntimeVersion)
	assert.Contains(spanTags, constants.RuntimeName)
	assert.Contains(spanTags, constants.GitRepositoryURL)
	assert.Contains(spanTags, constants.GitCommitSHA)
}

func TestTestSession(t *testing.T) {
	mockTracer.Reset()
	assert := assert.New(t)

	now := time.Now()
	session := createDDTestSession(now)
	assert.NotNil(session.Context())
	assert.Equal("my-command", session.Command())
	assert.Equal("/tmp/wd", session.WorkingDirectory())
	assert.Equal("my-testing-framework", session.Framework())
	assert.Equal(now.Unix(), session.StartTime().Unix())

	session.Close(42)

	finishedSpans := mockTracer.FinishedSpans()
	assert.Equal(1, len(finishedSpans))
	sessionAssertions(assert, now, finishedSpans[0])

	// session already closed, this is a no-op
	session.Close(0)
}

func sessionAssertions(assert *assert.Assertions, now time.Time, sessionSpan *mocktracer.Span) {
	assert.Equal(now.Unix(), sessionSpan.StartTime().Unix())
	assert.Equal("my-testing-framework.test_session", sessionSpan.OperationName())

	tags := map[string]interface{}{
		ext.ResourceName:              "my-testing-framework.test_session.my-command",
		ext.ErrorType:                 "ExitCode",
		ext.ErrorMsg:                  "exit code is not zero.",
		ext.SpanType:                  constants.SpanTypeTestSession,
		constants.TestStatus:          constants.TestStatusFail,
		constants.TestCommandExitCode: float64(42),
	}

	spanTags := sessionSpan.Tags()

	assert.Subset(spanTags, tags)
	assert.Contains(spanTags, constants.TestSessionIDTag)
	commonAssertions(assert, sessionSpan)
}

func TestTestModule(t *testing.T) {
	mockTracer.Reset()
	assert := assert.New(t)

	now := time.Now()
	session, module := createDDTestModule(now)
	defer func() { session.Close(0) }()
	module.SetError(WithErrorInfo("my-type", "my-message", "my-stack"))

	assert.NotNil(module.Context())
	assert.Equal("my-module", module.Name())
	assert.Equal("my-module-framework", module.Framework())
	assert.Equal(now.Unix(), module.StartTime().Unix())
	assert.Equal(session, module.Session())

	module.Close()

	finishedSpans := mockTracer.FinishedSpans()
	assert.Equal(1, len(finishedSpans))
	moduleAssertions(assert, now, finishedSpans[0])

	//no-op call
	module.Close()
}

func moduleAssertions(assert *assert.Assertions, now time.Time, moduleSpan *mocktracer.Span) {
	assert.Equal(now.Unix(), moduleSpan.StartTime().Unix())
	assert.Equal("my-module-framework.test_module", moduleSpan.OperationName())

	tags := map[string]interface{}{
		ext.ResourceName:     "my-module",
		ext.ErrorType:        "my-type",
		ext.ErrorMsg:         "my-message",
		ext.ErrorStack:       "my-stack",
		ext.SpanType:         constants.SpanTypeTestModule,
		constants.TestModule: "my-module",
	}

	spanTags := moduleSpan.Tags()

	assert.Subset(spanTags, tags)
	assert.Contains(spanTags, constants.TestSessionIDTag)
	assert.Contains(spanTags, constants.TestModuleIDTag)
	commonAssertions(assert, moduleSpan)
}

func TestTestSuite(t *testing.T) {
	mockTracer.Reset()
	assert := assert.New(t)

	now := time.Now()
	session, module, suite := createDDTestSuite(now)
	defer func() {
		session.Close(0)
		module.Close()
	}()
	suite.SetError(WithErrorInfo("my-type", "my-message", "my-stack"))

	assert.NotNil(suite.Context())
	assert.Equal("my-suite", suite.Name())
	assert.Equal(now.Unix(), suite.StartTime().Unix())
	assert.Equal(module, suite.Module())

	suite.Close()

	finishedSpans := mockTracer.FinishedSpans()
	assert.Equal(1, len(finishedSpans))
	suiteAssertions(assert, now, finishedSpans[0])

	//no-op call
	suite.Close()
}

func suiteAssertions(assert *assert.Assertions, now time.Time, suiteSpan *mocktracer.Span) {
	assert.Equal(now.Unix(), suiteSpan.StartTime().Unix())
	assert.Equal("my-module-framework.test_suite", suiteSpan.OperationName())

	tags := map[string]interface{}{
		ext.ResourceName:     "my-suite",
		ext.ErrorType:        "my-type",
		ext.ErrorMsg:         "my-message",
		ext.ErrorStack:       "my-stack",
		ext.SpanType:         constants.SpanTypeTestSuite,
		constants.TestModule: "my-module",
		constants.TestSuite:  "my-suite",
	}

	spanTags := suiteSpan.Tags()

	assert.Subset(spanTags, tags)
	assert.Contains(spanTags, constants.TestSessionIDTag)
	assert.Contains(spanTags, constants.TestModuleIDTag)
	assert.Contains(spanTags, constants.TestSuiteIDTag)
	commonAssertions(assert, suiteSpan)
}

func TestTest(t *testing.T) {
	mockTracer.Reset()
	assert := assert.New(t)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()
	test.SetError(WithError(errors.New("we keep the last error")))
	test.SetError(WithErrorInfo("my-type", "my-message", "my-stack"))
	pc, _, _, _ := runtime.Caller(0)
	test.SetTestFunc(runtime.FuncForPC(pc))

	assert.NotNil(test.Context())
	assert.Equal("my-test", test.Name())
	assert.Equal(now.Unix(), test.StartTime().Unix())
	assert.Equal(suite, test.Suite())

	test.Close(ResultStatusPass)

	finishedSpans := mockTracer.FinishedSpans()
	assert.Equal(1, len(finishedSpans))
	testAssertions(assert, now, finishedSpans[0])

	//no-op call
	test.Close(ResultStatusSkip)
}

func TestWithInnerFunc(t *testing.T) {
	mockTracer.Reset()
	assert := assert.New(t)

	now := time.Now()
	session, module, suite, test := createDDTest(now)
	defer func() {
		session.Close(0)
		module.Close()
		suite.Close()
	}()
	test.SetError(WithError(errors.New("we keep the last error")))
	test.SetError(WithErrorInfo("my-type", "my-message", "my-stack"))
	func() {
		pc, _, _, _ := runtime.Caller(0)
		test.SetTestFunc(runtime.FuncForPC(pc))
	}()

	assert.NotNil(test.Context())
	assert.Equal("my-test", test.Name())
	assert.Equal(now, test.StartTime())
	assert.Equal(suite, test.Suite())

	test.Close(ResultStatusPass)

	finishedSpans := mockTracer.FinishedSpans()
	assert.Equal(1, len(finishedSpans))
	testAssertions(assert, now, finishedSpans[0])

	//no-op call
	test.Close(ResultStatusSkip)
}

func testAssertions(assert *assert.Assertions, now time.Time, testSpan *mocktracer.Span) {
	assert.Equal(now.Unix(), testSpan.StartTime().Unix())
	assert.Equal("my-module-framework.test", testSpan.OperationName())

	tags := map[string]interface{}{
		ext.ResourceName:     "my-suite.my-test",
		ext.ErrorType:        "my-type",
		ext.ErrorMsg:         "my-message",
		ext.ErrorStack:       "my-stack",
		ext.SpanType:         constants.SpanTypeTest,
		constants.TestModule: "my-module",
		constants.TestSuite:  "my-suite",
		constants.TestName:   "my-test",
		constants.TestStatus: constants.TestStatusPass,
	}

	spanTags := testSpan.Tags()

	assert.Subset(spanTags, tags)
	assert.Contains(spanTags, constants.TestSessionIDTag)
	assert.Contains(spanTags, constants.TestModuleIDTag)
	assert.Contains(spanTags, constants.TestSuiteIDTag)
	assert.Contains(spanTags, constants.TestSourceFile)

	// make sure we have both start and end line
	assert.Contains(spanTags, constants.TestSourceStartLine)
	assert.Contains(spanTags, constants.TestSourceEndLine)

	// make sure the startLine < endLine
	if startLine, startLineOk := spanTags[constants.TestSourceStartLine].(float64); startLineOk {
		if endLine, endLineOk := spanTags[constants.TestSourceEndLine].(float64); endLineOk {
			assert.Less(startLine, endLine)
		}
	}

	commonAssertions(assert, testSpan)
}
