// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
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

	modules map[string]DdTestModule
}

// CreateTestSession initializes a new test session. It automatically determines the command and working directory.
func CreateTestSession() DdTestSession {
	var cmd string
	if len(os.Args) == 1 {
		cmd = filepath.Base(os.Args[0])
	} else {
		cmd = fmt.Sprintf("%s %s ", filepath.Base(os.Args[0]), strings.Join(os.Args[1:], " "))
	}

	// Filter out some parameters to make the command more stable.
	cmd = regexp.MustCompile(`(?si)-test.gocoverdir=(.*)\s`).ReplaceAllString(cmd, "")
	cmd = regexp.MustCompile(`(?si)-test.v=(.*)\s`).ReplaceAllString(cmd, "")
	cmd = regexp.MustCompile(`(?si)-test.testlogfile=(.*)\s`).ReplaceAllString(cmd, "")
	cmd = strings.TrimSpace(cmd)
	wd, err := os.Getwd()
	if err == nil {
		wd = utils.GetRelativePathFromCITagsSourceRoot(wd)
	}
	return CreateTestSessionWith(cmd, wd, "", time.Now())
}

// CreateTestSessionWith initializes a new test session with specified command, working directory, framework, and start time.
func CreateTestSessionWith(command string, workingDirectory string, framework string, startTime time.Time) DdTestSession {
	// Ensure CI visibility is properly configured.
	EnsureCiVisibilityInitialization()

	operationName := "test_session"
	if framework != "" {
		operationName = fmt.Sprintf("%s.%s", strings.ToLower(framework), operationName)
	}

	resourceName := fmt.Sprintf("%s.%s", operationName, command)

	sessionTags := []tracer.StartSpanOption{
		tracer.Tag(constants.TestType, constants.TestTypeTest),
		tracer.Tag(constants.TestCommand, command),
		tracer.Tag(constants.TestCommandWorkingDirectory, workingDirectory),
	}

	testOpts := append(fillCommonTags([]tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(constants.SpanTypeTestSession),
		tracer.StartTime(startTime),
	}), sessionTags...)

	span, ctx := tracer.StartSpanFromContext(context.Background(), operationName, testOpts...)
	sessionID := span.Context().SpanID()
	span.SetTag(constants.TestSessionIDTag, fmt.Sprint(sessionID))

	s := &tslvTestSession{
		sessionID:        sessionID,
		command:          command,
		workingDirectory: workingDirectory,
		framework:        framework,
		modules:          map[string]DdTestModule{},
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: startTime,
			tags:      sessionTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Ensure to close everything before CI visibility exits. In CI visibility mode, we try to never lose data.
	PushCiVisibilityCloseAction(func() { s.Close(1) })

	return s
}

// Command returns the command used to run the test session.
func (t *tslvTestSession) Command() string { return t.command }

// Framework returns the testing framework used in the test session.
func (t *tslvTestSession) Framework() string { return t.framework }

// WorkingDirectory returns the working directory of the test session.
func (t *tslvTestSession) WorkingDirectory() string { return t.workingDirectory }

// Close closes the test session with the given exit code and sets the finish time to the current time.
func (t *tslvTestSession) Close(exitCode int) { t.CloseWithFinishTime(exitCode, time.Now()) }

// CloseWithFinishTime closes the test session with the given exit code and finish time.
func (t *tslvTestSession) CloseWithFinishTime(exitCode int, finishTime time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		return
	}

	for _, m := range t.modules {
		m.Close()
	}
	t.modules = map[string]DdTestModule{}

	t.span.SetTag(constants.TestCommandExitCode, exitCode)
	if exitCode == 0 {
		t.span.SetTag(constants.TestStatus, constants.TestStatusPass)
	} else {
		t.SetErrorInfo("ExitCode", "exit code is not zero.", "")
		t.span.SetTag(constants.TestStatus, constants.TestStatusFail)
	}

	t.span.Finish(tracer.FinishTime(finishTime))
	t.closed = true

	tracer.Flush()
}

// GetOrCreateModule returns an existing module or creates a new one with the given name.
func (t *tslvTestSession) GetOrCreateModule(name string) DdTestModule {
	return t.GetOrCreateModuleWithFramework(name, "", "")
}

// GetOrCreateModuleWithFramework returns an existing module or creates a new one with the given name, framework, and framework version.
func (t *tslvTestSession) GetOrCreateModuleWithFramework(name string, framework string, frameworkVersion string) DdTestModule {
	return t.GetOrCreateModuleWithFrameworkAndStartTime(name, framework, frameworkVersion, time.Now())
}

// GetOrCreateModuleWithFrameworkAndStartTime returns an existing module or creates a new one with the given name, framework, framework version, and start time.
func (t *tslvTestSession) GetOrCreateModuleWithFrameworkAndStartTime(name string, framework string, frameworkVersion string, startTime time.Time) DdTestModule {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	var mod DdTestModule
	if v, ok := t.modules[name]; ok {
		mod = v
	} else {
		mod = createTestModule(t, name, framework, frameworkVersion, startTime)
		t.modules[name] = mod
	}

	return mod
}
