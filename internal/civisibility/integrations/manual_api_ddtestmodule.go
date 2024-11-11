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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)

// Test Module

// Ensures that tslvTestModule implements the TestModule interface.
var _ TestModule = (*tslvTestModule)(nil)

// tslvTestModule implements the DdTestModule interface and represents a module within a test session.
type tslvTestModule struct {
	ciVisibilityCommon
	session   *tslvTestSession
	moduleID  uint64
	name      string
	framework string

	suites map[string]TestSuite
}

// createTestModule initializes a new test module within a given session.
func createTestModule(session *tslvTestSession, name string, framework string, frameworkVersion string, startTime time.Time) TestModule {
	// Ensure CI visibility is properly configured.
	EnsureCiVisibilityInitialization()

	operationName := "test_module"
	if framework != "" {
		operationName = fmt.Sprintf("%s.%s", strings.ToLower(framework), operationName)
	}

	resourceName := name

	var sessionTags []tracer.StartSpanOption
	if session != nil {
		sessionTags = session.tags
	}

	// Module tags should include session tags so the backend can calculate the session fingerprint from the module.
	moduleTags := append(sessionTags, []tracer.StartSpanOption{
		tracer.Tag(constants.TestType, constants.TestTypeTest),
		tracer.Tag(constants.TestModule, name),
		tracer.Tag(constants.TestFramework, framework),
		tracer.Tag(constants.TestFrameworkVersion, frameworkVersion),
	}...)

	testOpts := append(fillCommonTags([]tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(constants.SpanTypeTestModule),
		tracer.StartTime(startTime),
	}), moduleTags...)

	span, ctx := tracer.StartSpanFromContext(context.Background(), operationName, testOpts...)
	moduleID := span.Context().SpanID()
	if session != nil {
		span.SetTag(constants.TestSessionIDTag, fmt.Sprint(session.sessionID))
	}
	span.SetTag(constants.TestModuleIDTag, fmt.Sprint(moduleID))

	module := &tslvTestModule{
		session:   session,
		moduleID:  moduleID,
		name:      name,
		framework: framework,
		suites:    map[string]TestSuite{},
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: startTime,
			tags:      moduleTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Ensure to close everything before CI visibility exits. In CI visibility mode, we try to never lose data.
	PushCiVisibilityCloseAction(func() { module.Close() })

	// Creating telemetry event created
	telemetry.EventCreated(module.framework, telemetry.ModuleEventType)
	return module
}

// ModuleID returns the ID of the module.
func (t *tslvTestModule) ModuleID() uint64 {
	return t.moduleID
}

// Name returns the name of the test module.
func (t *tslvTestModule) Name() string { return t.name }

// Framework returns the testing framework used by the test module.
func (t *tslvTestModule) Framework() string { return t.framework }

// Session returns the test session to which the test module belongs.
func (t *tslvTestModule) Session() TestSession { return t.session }

// Close closes the test module.
func (t *tslvTestModule) Close(options ...TestModuleCloseOption) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}

	defaults := &tslvTestModuleCloseOptions{}
	for _, o := range options {
		o(defaults)
	}

	if defaults.finishTime.IsZero() {
		defaults.finishTime = time.Now()
	}

	for _, suite := range t.suites {
		suite.Close()
	}
	t.suites = map[string]TestSuite{}

	t.span.Finish(tracer.FinishTime(defaults.finishTime))
	t.closed = true

	// Creating telemetry event finished
	telemetry.EventFinished(t.framework, telemetry.ModuleEventType)
}

// GetOrCreateSuite returns an existing suite or creates a new one with the given name.
func (t *tslvTestModule) GetOrCreateSuite(name string, options ...TestSuiteStartOption) TestSuite {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	defaults := &tslvTestSuiteStartOptions{}
	for _, o := range options {
		o(defaults)
	}

	if defaults.startTime.IsZero() {
		defaults.startTime = time.Now()
	}

	var suite TestSuite
	if v, ok := t.suites[name]; ok {
		suite = v
	} else {
		suite = createTestSuite(t, name, defaults.startTime)
		t.suites[name] = suite
	}

	return suite
}
