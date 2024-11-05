// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
)

// Test Suite

// Ensures that tslvTestSuite implements the DdTestSuite interface.
var _ DdTestSuite = (*tslvTestSuite)(nil)

// tslvTestSuite implements the DdTestSuite interface and represents a suite of tests within a module.
type tslvTestSuite struct {
	ciVisibilityCommon
	module  *tslvTestModule
	suiteID uint64
	name    string
}

// createTestSuite initializes a new test suite within a given module.
func createTestSuite(module *tslvTestModule, name string, startTime time.Time) DdTestSuite {
	if module == nil {
		return nil
	}

	operationName := "test_suite"
	if module.framework != "" {
		operationName = fmt.Sprintf("%s.%s", strings.ToLower(module.framework), operationName)
	}

	resourceName := name

	// Suite tags should include module and session tags so the backend can calculate the module and session fingerprint from the suite.
	suiteTags := append(module.tags, tracer.Tag(constants.TestSuite, name))
	testOpts := append(fillCommonTags([]tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(constants.SpanTypeTestSuite),
		tracer.StartTime(startTime),
	}), suiteTags...)

	span, ctx := tracer.StartSpanFromContext(context.Background(), operationName, testOpts...)
	suiteID := span.Context().SpanID()
	if module.session != nil {
		span.SetTag(constants.TestSessionIDTag, fmt.Sprint(module.session.sessionID))
	}
	span.SetTag(constants.TestModuleIDTag, fmt.Sprint(module.moduleID))
	span.SetTag(constants.TestSuiteIDTag, fmt.Sprint(suiteID))

	suite := &tslvTestSuite{
		module:  module,
		suiteID: suiteID,
		name:    name,
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: startTime,
			tags:      suiteTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Ensure to close everything before CI visibility exits. In CI visibility mode, we try to never lose data.
	PushCiVisibilityCloseAction(func() { suite.Close() })

	return suite
}

// SuiteID returns the ID of the test suite.
func (t *tslvTestSuite) SuiteID() uint64 {
	return t.suiteID
}

// Name returns the name of the test suite.
func (t *tslvTestSuite) Name() string { return t.name }

// Module returns the module to which the test suite belongs.
func (t *tslvTestSuite) Module() DdTestModule { return t.module }

// DdTestSuiteCloseOption represents an option for closing a test suite.
type DdTestSuiteCloseOption func(*tslvTestSuiteCloseOptions)

// tslvTestSuiteCloseOptions represents the options for closing a test suite.
type tslvTestSuiteCloseOptions struct {
	finishTime time.Time
}

// WithTestSuiteFinishTime sets the finish time for closing the test suite.
func WithTestSuiteFinishTime(finishTime time.Time) DdTestSuiteCloseOption {
	return func(o *tslvTestSuiteCloseOptions) {
		o.finishTime = finishTime
	}
}

// Close closes the test suite with the given finish time.
func (t *tslvTestSuite) Close(options ...DdTestSuiteCloseOption) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}

	defaults := &tslvTestSuiteCloseOptions{}
	for _, opt := range options {
		opt(defaults)
	}

	if defaults.finishTime.IsZero() {
		defaults.finishTime = time.Now()
	}

	t.span.Finish(tracer.FinishTime(defaults.finishTime))
	t.closed = true
}

// SetError sets an error on the test suite and marks the module as having an error.
func (t *tslvTestSuite) SetError(options ...DdErrorOption) {
	t.ciVisibilityCommon.SetError(options...)
	t.Module().SetTag(ext.Error, true)
}

// DdTestStartOption represents an option for starting a test.
type DdTestStartOption func(*tslvTestStartOptions)

// tslvTestStartOptions represents the options for starting a test.
type tslvTestStartOptions struct {
	startTime time.Time
}

// WithTestStartTime sets the start time for starting a test.
func WithTestStartTime(startTime time.Time) DdTestStartOption {
	return func(o *tslvTestStartOptions) {
		o.startTime = startTime
	}
}

// CreateTest creates a new test within the suite.
func (t *tslvTestSuite) CreateTest(name string, options ...DdTestStartOption) DdTest {
	defaults := &tslvTestStartOptions{}
	for _, opt := range options {
		opt(defaults)
	}

	if defaults.startTime.IsZero() {
		defaults.startTime = time.Now()
	}

	return createTest(t, name, defaults.startTime)
}
