// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"runtime"
	"time"
)

// TestResultStatus represents the result status of a test.
type TestResultStatus int

const (
	// ResultStatusPass indicates that the test has passed.
	ResultStatusPass TestResultStatus = 0

	// ResultStatusFail indicates that the test has failed.
	ResultStatusFail TestResultStatus = 1

	// ResultStatusSkip indicates that the test has been skipped.
	ResultStatusSkip TestResultStatus = 2
)

// ErrorOption is a function that sets an option for creating an error.
type ErrorOption func(*tslvErrorOptions)

// tslvErrorOptions is a struct that holds options for creating an error.
type tslvErrorOptions struct {
	err       error
	errType   string
	message   string
	callstack string
}

// WithError sets the error on the options.
func WithError(err error) ErrorOption {
	return func(o *tslvErrorOptions) { o.err = err }
}

// WithErrorInfo sets detailed error information on the options.
func WithErrorInfo(errType string, message string, callstack string) ErrorOption {
	return func(o *tslvErrorOptions) {
		o.errType = errType
		o.message = message
		o.callstack = callstack
	}
}

// ddTslvEvent is an interface that provides common methods for CI visibility events.
type ddTslvEvent interface {
	// Context returns the context of the event.
	Context() context.Context

	// StartTime returns the start time of the event.
	StartTime() time.Time

	// SetError sets an error on the event.
	SetError(options ...ErrorOption)

	// SetTag sets a tag on the event.
	SetTag(key string, value interface{})
}

// TestSessionStartOption represents an option that can be passed to CreateTestSession.
type TestSessionStartOption func(*tslvTestSessionStartOptions)

// tslvTestSessionStartOptions contains the options for creating a new test session.
type tslvTestSessionStartOptions struct {
	command          string
	workingDirectory string
	framework        string
	frameworkVersion string
	startTime        time.Time
}

// WithTestSessionCommand sets the command used to run the test session.
func WithTestSessionCommand(command string) TestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) { o.command = command }
}

// WithTestSessionWorkingDirectory sets the working directory of the test session.
func WithTestSessionWorkingDirectory(workingDirectory string) TestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) { o.workingDirectory = workingDirectory }
}

// WithTestSessionFramework sets the testing framework used in the test session.
func WithTestSessionFramework(framework, frameworkVersion string) TestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) {
		o.framework = framework
		o.frameworkVersion = frameworkVersion
	}
}

// WithTestSessionStartTime sets the start time of the test session.
func WithTestSessionStartTime(startTime time.Time) TestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) { o.startTime = startTime }
}

// TestSessionCloseOption represents an option that can be passed to Close.
type TestSessionCloseOption func(*tslvTestSessionCloseOptions)

// tslvTestSessionCloseOptions contains the options for closing a test session.
type tslvTestSessionCloseOptions struct {
	finishTime time.Time
}

// WithTestSessionFinishTime sets the finish time of the test session.
func WithTestSessionFinishTime(finishTime time.Time) TestSessionCloseOption {
	return func(o *tslvTestSessionCloseOptions) { o.finishTime = finishTime }
}

// TestModuleStartOption represents an option that can be passed to GetOrCreateModule.
type TestModuleStartOption func(*tslvTestModuleStartOptions)

// tslvTestModuleOptions contains the options for creating a new test module.
type tslvTestModuleStartOptions struct {
	framework        string
	frameworkVersion string
	startTime        time.Time
}

// WithTestModuleFramework sets the testing framework used by the test module.
func WithTestModuleFramework(framework, frameworkVersion string) TestModuleStartOption {
	return func(o *tslvTestModuleStartOptions) {
		o.framework = framework
		o.frameworkVersion = frameworkVersion
	}
}

// WithTestModuleStartTime sets the start time of the test module.
func WithTestModuleStartTime(startTime time.Time) TestModuleStartOption {
	return func(o *tslvTestModuleStartOptions) { o.startTime = startTime }
}

// TestSession represents a session for a set of tests.
type TestSession interface {
	ddTslvEvent

	// SessionID returns the ID of the session.
	SessionID() uint64

	// Command returns the command used to run the session.
	Command() string

	// Framework returns the testing framework used.
	Framework() string

	// WorkingDirectory returns the working directory of the session.
	WorkingDirectory() string

	// Close closes the test session with the given exit code.
	Close(exitCode int, options ...TestSessionCloseOption)

	// GetOrCreateModule returns an existing module or creates a new one with the given name.
	GetOrCreateModule(name string, options ...TestModuleStartOption) TestModule
}

// TestModuleCloseOption represents an option for closing a test module.
type TestModuleCloseOption func(*tslvTestModuleCloseOptions)

// tslvTestModuleCloseOptions represents the options for closing a test module.
type tslvTestModuleCloseOptions struct {
	finishTime time.Time
}

// WithTestModuleFinishTime sets the finish time for closing the test module.
func WithTestModuleFinishTime(finishTime time.Time) TestModuleCloseOption {
	return func(o *tslvTestModuleCloseOptions) { o.finishTime = finishTime }
}

// TestSuiteStartOption represents an option for starting a test suite.
type TestSuiteStartOption func(*tslvTestSuiteStartOptions)

// tslvTestSuiteStartOptions represents the options for starting a test suite.
type tslvTestSuiteStartOptions struct {
	startTime time.Time
}

// WithTestSuiteStartTime sets the start time for starting a test suite.
func WithTestSuiteStartTime(startTime time.Time) TestSuiteStartOption {
	return func(o *tslvTestSuiteStartOptions) { o.startTime = startTime }
}

// TestModule represents a module within a test session.
type TestModule interface {
	ddTslvEvent

	// ModuleID returns the ID of the module.
	ModuleID() uint64

	// Session returns the test session to which the module belongs.
	Session() TestSession

	// Framework returns the testing framework used by the module.
	Framework() string

	// Name returns the name of the module.
	Name() string

	// Close closes the test module.
	Close(options ...TestModuleCloseOption)

	// GetOrCreateSuite returns an existing suite or creates a new one with the given name.
	GetOrCreateSuite(name string, options ...TestSuiteStartOption) TestSuite
}

// TestSuiteCloseOption represents an option for closing a test suite.
type TestSuiteCloseOption func(*tslvTestSuiteCloseOptions)

// tslvTestSuiteCloseOptions represents the options for closing a test suite.
type tslvTestSuiteCloseOptions struct {
	finishTime time.Time
}

// WithTestSuiteFinishTime sets the finish time for closing the test suite.
func WithTestSuiteFinishTime(finishTime time.Time) TestSuiteCloseOption {
	return func(o *tslvTestSuiteCloseOptions) { o.finishTime = finishTime }
}

// TestStartOption represents an option for starting a test.
type TestStartOption func(*tslvTestStartOptions)

// tslvTestStartOptions represents the options for starting a test.
type tslvTestStartOptions struct {
	startTime time.Time
}

// WithTestStartTime sets the start time for starting a test.
func WithTestStartTime(startTime time.Time) TestStartOption {
	return func(o *tslvTestStartOptions) { o.startTime = startTime }
}

// TestSuite represents a suite of tests within a module.
type TestSuite interface {
	ddTslvEvent

	// SuiteID returns the ID of the suite.
	SuiteID() uint64

	// Module returns the module to which the suite belongs.
	Module() TestModule

	// Name returns the name of the suite.
	Name() string

	// Close closes the test suite.
	Close(options ...TestSuiteCloseOption)

	// CreateTest creates a new test with the given name and options.
	CreateTest(name string, options ...TestStartOption) Test
}

// TestCloseOption represents an option for closing a test.
type TestCloseOption func(*tslvTestCloseOptions)

// tslvTestCloseOptions represents the options for closing a test.
type tslvTestCloseOptions struct {
	finishTime time.Time
	skipReason string
}

// WithTestFinishTime sets the finish time of the test.
func WithTestFinishTime(finishTime time.Time) TestCloseOption {
	return func(o *tslvTestCloseOptions) { o.finishTime = finishTime }
}

// WithTestSkipReason sets the skip reason of the test.
func WithTestSkipReason(skipReason string) TestCloseOption {
	return func(o *tslvTestCloseOptions) { o.skipReason = skipReason }
}

// Test represents an individual test within a suite.
type Test interface {
	ddTslvEvent

	// TestID returns the ID of the test.
	TestID() uint64

	// Name returns the name of the test.
	Name() string

	// Suite returns the suite to which the test belongs.
	Suite() TestSuite

	// Close closes the test with the given status.
	Close(status TestResultStatus, options ...TestCloseOption)

	// SetTestFunc sets the function to be tested. (Sets the test.source tags and test.codeowners)
	SetTestFunc(fn *runtime.Func)

	// SetBenchmarkData sets benchmark data for the test.
	SetBenchmarkData(measureType string, data map[string]any)
}
