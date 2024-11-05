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

// DdErrorOption is a function that sets an option for creating an error.
type DdErrorOption func(*tslvErrorOptions)

// tslvErrorOptions is a struct that holds options for creating an error.
type tslvErrorOptions struct {
	err       error
	errType   string
	message   string
	callstack string
}

// WithError sets the error on the options.
func WithError(err error) DdErrorOption {
	return func(o *tslvErrorOptions) { o.err = err }
}

// WithErrorInfo sets detailed error information on the options.
func WithErrorInfo(errType string, message string, callstack string) DdErrorOption {
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
	SetError(options ...DdErrorOption)

	// SetTag sets a tag on the event.
	SetTag(key string, value interface{})
}

// DdTestSessionStartOption represents an option that can be passed to CreateTestSession.
type DdTestSessionStartOption func(*tslvTestSessionStartOptions)

// tslvTestSessionStartOptions contains the options for creating a new test session.
type tslvTestSessionStartOptions struct {
	command          string
	workingDirectory string
	framework        string
	frameworkVersion string
	startTime        time.Time
}

// WithTestSessionCommand sets the command used to run the test session.
func WithTestSessionCommand(command string) DdTestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) { o.command = command }
}

// WithTestSessionWorkingDirectory sets the working directory of the test session.
func WithTestSessionWorkingDirectory(workingDirectory string) DdTestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) { o.workingDirectory = workingDirectory }
}

// WithTestSessionFramework sets the testing framework used in the test session.
func WithTestSessionFramework(framework, frameworkVersion string) DdTestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) {
		o.framework = framework
		o.frameworkVersion = frameworkVersion
	}
}

// WithTestSessionStartTime sets the start time of the test session.
func WithTestSessionStartTime(startTime time.Time) DdTestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) { o.startTime = startTime }
}

// DdTestSessionCloseOption represents an option that can be passed to Close.
type DdTestSessionCloseOption func(*tslvTestSessionCloseOptions)

// tslvTestSessionCloseOptions contains the options for closing a test session.
type tslvTestSessionCloseOptions struct {
	finishTime time.Time
}

// WithTestSessionFinishTime sets the finish time of the test session.
func WithTestSessionFinishTime(finishTime time.Time) DdTestSessionCloseOption {
	return func(o *tslvTestSessionCloseOptions) { o.finishTime = finishTime }
}

// DdTestModuleStartOption represents an option that can be passed to GetOrCreateModule.
type DdTestModuleStartOption func(*tslvTestModuleStartOptions)

// tslvTestModuleOptions contains the options for creating a new test module.
type tslvTestModuleStartOptions struct {
	framework        string
	frameworkVersion string
	startTime        time.Time
}

// WithTestModuleFramework sets the testing framework used by the test module.
func WithTestModuleFramework(framework, frameworkVersion string) DdTestModuleStartOption {
	return func(o *tslvTestModuleStartOptions) {
		o.framework = framework
		o.frameworkVersion = frameworkVersion
	}
}

// WithTestModuleStartTime sets the start time of the test module.
func WithTestModuleStartTime(startTime time.Time) DdTestModuleStartOption {
	return func(o *tslvTestModuleStartOptions) { o.startTime = startTime }
}

// DdTestSession represents a session for a set of tests.
type DdTestSession interface {
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
	Close(exitCode int, options ...DdTestSessionCloseOption)

	// GetOrCreateModule returns an existing module or creates a new one with the given name.
	GetOrCreateModule(name string, options ...DdTestModuleStartOption) DdTestModule
}

// DdTestModuleCloseOption represents an option for closing a test module.
type DdTestModuleCloseOption func(*tslvTestModuleCloseOptions)

// tslvTestModuleCloseOptions represents the options for closing a test module.
type tslvTestModuleCloseOptions struct {
	finishTime time.Time
}

// WithTestModuleFinishTime sets the finish time for closing the test module.
func WithTestModuleFinishTime(finishTime time.Time) DdTestModuleCloseOption {
	return func(o *tslvTestModuleCloseOptions) { o.finishTime = finishTime }
}

// DdTestSuiteStartOption represents an option for starting a test suite.
type DdTestSuiteStartOption func(*tslvTestSuiteStartOptions)

// tslvTestSuiteStartOptions represents the options for starting a test suite.
type tslvTestSuiteStartOptions struct {
	startTime time.Time
}

// WithTestSuiteStartTime sets the start time for starting a test suite.
func WithTestSuiteStartTime(startTime time.Time) DdTestSuiteStartOption {
	return func(o *tslvTestSuiteStartOptions) { o.startTime = startTime }
}

// DdTestModule represents a module within a test session.
type DdTestModule interface {
	ddTslvEvent

	// ModuleID returns the ID of the module.
	ModuleID() uint64

	// Session returns the test session to which the module belongs.
	Session() DdTestSession

	// Framework returns the testing framework used by the module.
	Framework() string

	// Name returns the name of the module.
	Name() string

	// Close closes the test module.
	Close(options ...DdTestModuleCloseOption)

	// GetOrCreateSuite returns an existing suite or creates a new one with the given name.
	GetOrCreateSuite(name string, options ...DdTestSuiteStartOption) DdTestSuite
}

// DdTestSuiteCloseOption represents an option for closing a test suite.
type DdTestSuiteCloseOption func(*tslvTestSuiteCloseOptions)

// tslvTestSuiteCloseOptions represents the options for closing a test suite.
type tslvTestSuiteCloseOptions struct {
	finishTime time.Time
}

// WithTestSuiteFinishTime sets the finish time for closing the test suite.
func WithTestSuiteFinishTime(finishTime time.Time) DdTestSuiteCloseOption {
	return func(o *tslvTestSuiteCloseOptions) { o.finishTime = finishTime }
}

// DdTestStartOption represents an option for starting a test.
type DdTestStartOption func(*tslvTestStartOptions)

// tslvTestStartOptions represents the options for starting a test.
type tslvTestStartOptions struct {
	startTime time.Time
}

// WithTestStartTime sets the start time for starting a test.
func WithTestStartTime(startTime time.Time) DdTestStartOption {
	return func(o *tslvTestStartOptions) { o.startTime = startTime }
}

// DdTestSuite represents a suite of tests within a module.
type DdTestSuite interface {
	ddTslvEvent

	// SuiteID returns the ID of the suite.
	SuiteID() uint64

	// Module returns the module to which the suite belongs.
	Module() DdTestModule

	// Name returns the name of the suite.
	Name() string

	// Close closes the test suite.
	Close(options ...DdTestSuiteCloseOption)

	// CreateTest creates a new test with the given name and options.
	CreateTest(name string, options ...DdTestStartOption) DdTest
}

// DdTestCloseOption represents an option for closing a test.
type DdTestCloseOption func(*tslvTestCloseOptions)

// tslvTestCloseOptions represents the options for closing a test.
type tslvTestCloseOptions struct {
	finishTime time.Time
	skipReason string
}

// WithTestFinishTime sets the finish time of the test.
func WithTestFinishTime(finishTime time.Time) DdTestCloseOption {
	return func(o *tslvTestCloseOptions) { o.finishTime = finishTime }
}

// WithTestSkipReason sets the skip reason of the test.
func WithTestSkipReason(skipReason string) DdTestCloseOption {
	return func(o *tslvTestCloseOptions) { o.skipReason = skipReason }
}

// DdTest represents an individual test within a suite.
type DdTest interface {
	ddTslvEvent

	// TestID returns the ID of the test.
	TestID() uint64

	// Name returns the name of the test.
	Name() string

	// Suite returns the suite to which the test belongs.
	Suite() DdTestSuite

	// Close closes the test with the given status.
	Close(status TestResultStatus, options ...DdTestCloseOption)

	// SetTestFunc sets the function to be tested. (Sets the test.source tags and test.codeowners)
	SetTestFunc(fn *runtime.Func)

	// SetBenchmarkData sets benchmark data for the test.
	SetBenchmarkData(measureType string, data map[string]any)
}
