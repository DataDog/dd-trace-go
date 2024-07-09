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
)

// Test Module

// Ensures that tslvTestModule implements the DdTestModule interface.
var _ DdTestModule = (*tslvTestModule)(nil)

// tslvTestModule implements the DdTestModule interface and represents a module within a test session.
type tslvTestModule struct {
	ciVisibilityCommon
	session   *tslvTestSession
	moduleID  uint64
	name      string
	framework string

	suites map[string]DdTestSuite
}

// createTestModule initializes a new test module within a given session.
func createTestModule(session *tslvTestSession, name string, framework string, frameworkVersion string, startTime time.Time) DdTestModule {
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
		suites:    map[string]DdTestSuite{},
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: startTime,
			tags:      moduleTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Ensure to close everything before CI visibility exits. In CI visibility mode, we try to never lose data.
	PushCiVisibilityCloseAction(func() { module.Close() })

	return module
}

// Name returns the name of the test module.
func (t *tslvTestModule) Name() string { return t.name }

// Framework returns the testing framework used by the test module.
func (t *tslvTestModule) Framework() string { return t.framework }

// Session returns the test session to which the test module belongs.
func (t *tslvTestModule) Session() DdTestSession { return t.session }

// Close closes the test module and sets the finish time to the current time.
func (t *tslvTestModule) Close() { t.CloseWithFinishTime(time.Now()) }

// CloseWithFinishTime closes the test module with the given finish time.
func (t *tslvTestModule) CloseWithFinishTime(finishTime time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}

	for _, suite := range t.suites {
		suite.Close()
	}
	t.suites = map[string]DdTestSuite{}

	t.span.Finish(tracer.FinishTime(finishTime))
	t.closed = true
}

// GetOrCreateSuite returns an existing suite or creates a new one with the given name.
func (t *tslvTestModule) GetOrCreateSuite(name string) DdTestSuite {
	return t.GetOrCreateSuiteWithStartTime(name, time.Now())
}

// GetOrCreateSuiteWithStartTime returns an existing suite or creates a new one with the given name and start time.
func (t *tslvTestModule) GetOrCreateSuiteWithStartTime(name string, startTime time.Time) DdTestSuite {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	var suite DdTestSuite
	if v, ok := t.suites[name]; ok {
		suite = v
	} else {
		suite = createTestSuite(t, name, startTime)
		t.suites[name] = suite
	}

	return suite
}
