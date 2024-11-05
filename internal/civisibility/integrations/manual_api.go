// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"runtime"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
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

// ddTslvEvent is an interface that provides common methods for CI visibility events.
type ddTslvEvent interface {
	// Context returns the context of the event.
	Context() context.Context

	// StartTime returns the start time of the event.
	StartTime() time.Time

	// SetError sets an error on the event.
	SetError(err error)

	// SetErrorInfo sets detailed error information on the event.
	SetErrorInfo(errType string, message string, callstack string)

	// SetTag sets a tag on the event.
	SetTag(key string, value interface{})
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

// common
var _ ddTslvEvent = (*ciVisibilityCommon)(nil)

// ciVisibilityCommon is a struct that implements the ddTslvEvent interface and provides common functionality for CI visibility.
type ciVisibilityCommon struct {
	startTime time.Time

	tags   []tracer.StartSpanOption
	span   tracer.Span
	ctx    context.Context
	mutex  sync.Mutex
	closed bool
}

// Context returns the context of the event.
func (c *ciVisibilityCommon) Context() context.Context { return c.ctx }

// StartTime returns the start time of the event.
func (c *ciVisibilityCommon) StartTime() time.Time { return c.startTime }

// SetError sets an error on the event.
func (c *ciVisibilityCommon) SetError(err error) {
	c.span.SetTag(ext.Error, err)
}

// SetErrorInfo sets detailed error information on the event.
func (c *ciVisibilityCommon) SetErrorInfo(errType string, message string, callstack string) {
	// set the span with error:1
	c.span.SetTag(ext.Error, true)

	// set the error type
	if errType != "" {
		c.span.SetTag(ext.ErrorType, errType)
	}

	// set the error message
	if message != "" {
		c.span.SetTag(ext.ErrorMsg, message)
	}

	// set the error stacktrace
	if callstack != "" {
		c.span.SetTag(ext.ErrorStack, callstack)
	}
}

// SetTag sets a tag on the event.
func (c *ciVisibilityCommon) SetTag(key string, value interface{}) { c.span.SetTag(key, value) }

// fillCommonTags adds common tags to the span options for CI visibility.
func fillCommonTags(opts []tracer.StartSpanOption) []tracer.StartSpanOption {
	opts = append(opts, []tracer.StartSpanOption{
		tracer.Tag(constants.Origin, constants.CIAppTestOrigin),
		tracer.Tag(ext.ManualKeep, true),
	}...)

	// Apply CI tags
	for k, v := range utils.GetCITags() {
		// Ignore the test session name (sent at the payload metadata level, see `civisibility_payload.go`)
		if k == constants.TestSessionName {
			continue
		}
		opts = append(opts, tracer.Tag(k, v))
	}

	// Apply CI metrics
	for k, v := range utils.GetCIMetrics() {
		opts = append(opts, tracer.Tag(k, v))
	}

	return opts
}
