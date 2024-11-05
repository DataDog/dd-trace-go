// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)

// Test Session

// Ensures that tslvTestSession implements the DdTestSession interface.
var _ DdTestSession = (*tslvTestSession)(nil)

// tslvTestSession implements the DdTestSession interface and represents a session for a set of tests.
type tslvTestSession struct {
	ciVisibilityCommon
	sessionID        uint64
	command          string
	workingDirectory string
	framework        string
	frameworkVersion string

	modules map[string]DdTestModule
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
	return func(o *tslvTestSessionStartOptions) {
		o.command = command
	}
}

// WithTestSessionWorkingDirectory sets the working directory of the test session.
func WithTestSessionWorkingDirectory(workingDirectory string) DdTestSessionStartOption {
	return func(o *tslvTestSessionStartOptions) {
		o.workingDirectory = workingDirectory
	}
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
	return func(o *tslvTestSessionStartOptions) {
		o.startTime = startTime
	}
}

// CreateTestSession initializes a new test session with the given command and working directory.
func CreateTestSession(options ...DdTestSessionStartOption) DdTestSession {
	defaults := &tslvTestSessionStartOptions{}
	for _, f := range options {
		f(defaults)
	}

	if defaults.command == "" {
		defaults.command = utils.GetCITags()[constants.TestCommand]
	}
	if defaults.workingDirectory == "" {
		wd, err := os.Getwd()
		if err == nil {
			wd = utils.GetRelativePathFromCITagsSourceRoot(wd)
		}
		defaults.workingDirectory = wd
	}
	if defaults.startTime.IsZero() {
		defaults.startTime = time.Now()
	}

	// Ensure CI visibility is properly configured.
	EnsureCiVisibilityInitialization()

	sessionTags := []tracer.StartSpanOption{
		tracer.Tag(constants.TestType, constants.TestTypeTest),
		tracer.Tag(constants.TestCommand, defaults.command),
		tracer.Tag(constants.TestCommandWorkingDirectory, defaults.workingDirectory),
	}

	operationName := "test_session"
	if defaults.framework != "" {
		operationName = fmt.Sprintf("%s.%s", strings.ToLower(defaults.framework), operationName)
		sessionTags = append(sessionTags,
			tracer.Tag(constants.TestFramework, defaults.framework),
			tracer.Tag(constants.TestFrameworkVersion, defaults.frameworkVersion))
	}

	resourceName := fmt.Sprintf("%s.%s", operationName, defaults.command)

	testOpts := append(fillCommonTags([]tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(constants.SpanTypeTestSession),
		tracer.StartTime(defaults.startTime),
	}), sessionTags...)

	span, ctx := tracer.StartSpanFromContext(context.Background(), operationName, testOpts...)
	sessionID := span.Context().SpanID()
	span.SetTag(constants.TestSessionIDTag, fmt.Sprint(sessionID))

	s := &tslvTestSession{
		sessionID:        sessionID,
		command:          defaults.command,
		workingDirectory: defaults.workingDirectory,
		framework:        defaults.framework,
		frameworkVersion: defaults.frameworkVersion,
		modules:          map[string]DdTestModule{},
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: defaults.startTime,
			tags:      sessionTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Ensure to close everything before CI visibility exits. In CI visibility mode, we try to never lose data.
	PushCiVisibilityCloseAction(func() { s.Close(1) })

	// Creating telemetry event created
	telemetry.EventCreated(s.framework, telemetry.GetSessionTestingEventType())
	return s
}

// SessionID returns the ID of the test session.
func (t *tslvTestSession) SessionID() uint64 {
	return t.sessionID
}

// Command returns the command used to run the test session.
func (t *tslvTestSession) Command() string { return t.command }

// Framework returns the testing framework used in the test session.
func (t *tslvTestSession) Framework() string { return t.framework }

// WorkingDirectory returns the working directory of the test session.
func (t *tslvTestSession) WorkingDirectory() string { return t.workingDirectory }

// DdTestSessionCloseOption represents an option that can be passed to Close.
type DdTestSessionCloseOption func(*tslvTestSessionCloseOptions)

// tslvTestSessionCloseOptions contains the options for closing a test session.
type tslvTestSessionCloseOptions struct {
	finishTime time.Time
}

// WithTestSessionFinishTime sets the finish time of the test session.
func WithTestSessionFinishTime(finishTime time.Time) DdTestSessionCloseOption {
	return func(o *tslvTestSessionCloseOptions) {
		o.finishTime = finishTime
	}
}

// Close closes the test session with the given exit code.
func (t *tslvTestSession) Close(exitCode int, options ...DdTestSessionCloseOption) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}

	defaults := &tslvTestSessionCloseOptions{}
	for _, f := range options {
		f(defaults)
	}

	if defaults.finishTime.IsZero() {
		defaults.finishTime = time.Now()
	}

	for _, m := range t.modules {
		m.Close()
	}
	t.modules = map[string]DdTestModule{}

	t.span.SetTag(constants.TestCommandExitCode, exitCode)
	if exitCode == 0 {
		t.span.SetTag(constants.TestStatus, constants.TestStatusPass)
	} else {
		t.SetError(WithErrorInfo("ExitCode", "exit code is not zero.", ""))
		t.span.SetTag(constants.TestStatus, constants.TestStatusFail)
	}

	t.span.Finish(tracer.FinishTime(defaults.finishTime))
	t.closed = true

	// Creating telemetry event finished
	telemetry.EventFinished(t.framework, telemetry.GetSessionTestingEventType())
	tracer.Flush()
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
	return func(o *tslvTestModuleStartOptions) {
		o.startTime = startTime
	}
}

// GetOrCreateModule returns an existing module or creates a new one with the given name, framework, framework version, and start time.
func (t *tslvTestSession) GetOrCreateModule(name string, options ...DdTestModuleStartOption) DdTestModule {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	defaults := &tslvTestModuleStartOptions{}
	for _, f := range options {
		f(defaults)
	}

	if defaults.framework == "" {
		defaults.framework = t.framework
		defaults.frameworkVersion = t.frameworkVersion
	}
	if defaults.startTime.IsZero() {
		defaults.startTime = time.Now()
	}

	var mod DdTestModule
	if v, ok := t.modules[name]; ok {
		mod = v
	} else {
		mod = createTestModule(t, name, defaults.framework, defaults.frameworkVersion, defaults.startTime)
		t.modules[name] = mod
	}

	return mod
}
