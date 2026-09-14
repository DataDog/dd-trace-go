// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internalenv "github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type processRetryChildTransportState struct {
	active bool
	values map[string]string
	err    error
}

var processRetryChildTransportKeys = [...]string{
	constants.CIVisibilityInternalRetryProcessChild,
	constants.CIVisibilityInternalRetryProcessResultPath,
	constants.CIVisibilityInternalRetryProcessTestName,
	constants.CIVisibilityInternalRetryProcessAttempt,
	constants.CIVisibilityInternalRetryProcessReason,
}

const processRetryChildTransportKeyPrefix = "DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_"

var processRetryChildTransport = initializeProcessRetryChildTransport()

// IsProcessRetryChild reports whether this process is executing a retry child.
// The integrations package snapshots and removes the transport marker during
// package initialization. Unrelated packages may initialize first, but TestMain,
// test bodies, and descendants started from them observe a scrubbed environment
// while these APIs continue to read the private snapshot.
func IsProcessRetryChild() bool {
	return processRetryChildTransport.active
}

// LookupProcessRetryChildTransport returns a private retry-process transport
// value from the immutable startup snapshot.
func LookupProcessRetryChildTransport(name string) (string, bool) {
	if !processRetryChildTransport.active {
		return "", false
	}
	key, ok := processRetryChildTransportKey(name)
	if !ok {
		return "", false
	}
	value, ok := processRetryChildTransport.values[key]
	return value, ok
}

// ProcessRetryChildTransportError returns the first error encountered while
// removing private transport keys from the live child environment.
func ProcessRetryChildTransportError() error {
	return processRetryChildTransport.err
}

// IsProcessRetryChildTransportKey reports whether name is one of the private
// parent-to-child retry transport keys.
func IsProcessRetryChildTransportKey(name string) bool {
	_, ok := processRetryChildTransportKey(name)
	return ok
}

func processRetryChildTransportKey(name string) (string, bool) {
	if len(name) < len(processRetryChildTransportKeyPrefix) ||
		!strings.EqualFold(name[:len(processRetryChildTransportKeyPrefix)], processRetryChildTransportKeyPrefix) {
		return "", false
	}
	for _, key := range processRetryChildTransportKeys {
		if strings.EqualFold(name, key) {
			return key, true
		}
	}
	return "", false
}

func initializeProcessRetryChildTransport() *processRetryChildTransportState {
	state, err := snapshotProcessRetryChildTransport(internalenv.Lookup, os.Unsetenv)
	if err != nil {
		log.Warn("civisibility: ignoring invalid process retry child transport and continuing normal test execution: %v", err.Error())
	}
	return state
}

func snapshotProcessRetryChildTransport(
	lookup func(string) (string, bool),
	unset func(string) error,
) (*processRetryChildTransportState, error) {
	state := &processRetryChildTransportState{}
	child, ok := lookup(constants.CIVisibilityInternalRetryProcessChild)
	if !ok {
		return state, nil
	}
	state.values = make(map[string]string, len(processRetryChildTransportKeys))
	for _, key := range processRetryChildTransportKeys {
		if value, ok := lookup(key); ok {
			state.values[key] = value
		}
		if err := unset(key); err != nil && state.err == nil {
			state.err = err
		}
	}

	enabled, err := strconv.ParseBool(strings.TrimSpace(child))
	if err != nil {
		state.values = nil
		return state, fmt.Errorf("invalid %s marker", constants.CIVisibilityInternalRetryProcessChild)
	}
	if !enabled {
		state.values = nil
		return state, nil
	}
	for _, key := range []string{
		constants.CIVisibilityInternalRetryProcessResultPath,
		constants.CIVisibilityInternalRetryProcessTestName,
		constants.CIVisibilityInternalRetryProcessReason,
	} {
		if strings.TrimSpace(state.values[key]) == "" {
			state.values = nil
			return state, fmt.Errorf("missing %s", key)
		}
	}
	attempt, err := strconv.Atoi(strings.TrimSpace(state.values[constants.CIVisibilityInternalRetryProcessAttempt]))
	if err != nil || attempt < 1 {
		state.values = nil
		return state, fmt.Errorf("invalid %s", constants.CIVisibilityInternalRetryProcessAttempt)
	}
	state.active = true
	return state, nil
}

var (
	_ TestSession       = (*processRetryNoopSession)(nil)
	_ TestModule        = (*processRetryNoopModule)(nil)
	_ TestSuite         = (*processRetryNoopSuite)(nil)
	_ Test              = (*processRetryNoopTest)(nil)
	_ mocktracer.Tracer = (*processRetryNoopMockTracer)(nil)
	_ tracer.Tracer     = (*processRetryNoopMockTracer)(nil)
)

type processRetryNoopSession struct {
	command          string
	workingDirectory string
	framework        string
	frameworkVersion string
}

func newProcessRetryNoopSession(options ...TestSessionStartOption) TestSession {
	cfg := &tslvTestSessionStartOptions{}
	for _, option := range options {
		option(cfg)
	}
	return &processRetryNoopSession{
		command:          cfg.command,
		workingDirectory: cfg.workingDirectory,
		framework:        cfg.framework,
		frameworkVersion: cfg.frameworkVersion,
	}
}

func (s *processRetryNoopSession) Context() context.Context             { return context.Background() }
func (s *processRetryNoopSession) StartTime() time.Time                 { return time.Time{} }
func (s *processRetryNoopSession) SetError(...ErrorOption)              {}
func (s *processRetryNoopSession) SetTag(string, any)                   {}
func (s *processRetryNoopSession) GetTag(string) (any, bool)            { return nil, false }
func (s *processRetryNoopSession) SessionID() uint64                    { return 0 }
func (s *processRetryNoopSession) Command() string                      { return s.command }
func (s *processRetryNoopSession) Framework() string                    { return s.framework }
func (s *processRetryNoopSession) WorkingDirectory() string             { return s.workingDirectory }
func (s *processRetryNoopSession) Close(int, ...TestSessionCloseOption) {}
func (s *processRetryNoopSession) GetOrCreateModule(name string, _ ...TestModuleStartOption) TestModule {
	return &processRetryNoopModule{session: s, name: name, framework: s.framework, frameworkVersion: s.frameworkVersion}
}

type processRetryNoopModule struct {
	session          TestSession
	name             string
	framework        string
	frameworkVersion string
}

func (m *processRetryNoopModule) Context() context.Context       { return context.Background() }
func (m *processRetryNoopModule) StartTime() time.Time           { return time.Time{} }
func (m *processRetryNoopModule) SetError(...ErrorOption)        {}
func (m *processRetryNoopModule) SetTag(string, any)             {}
func (m *processRetryNoopModule) GetTag(string) (any, bool)      { return nil, false }
func (m *processRetryNoopModule) ModuleID() uint64               { return 0 }
func (m *processRetryNoopModule) Session() TestSession           { return m.session }
func (m *processRetryNoopModule) Framework() string              { return m.framework }
func (m *processRetryNoopModule) Name() string                   { return m.name }
func (m *processRetryNoopModule) Close(...TestModuleCloseOption) {}
func (m *processRetryNoopModule) GetOrCreateSuite(name string, _ ...TestSuiteStartOption) TestSuite {
	return &processRetryNoopSuite{module: m, name: name}
}

type processRetryNoopSuite struct {
	module TestModule
	name   string
}

func (s *processRetryNoopSuite) Context() context.Context      { return context.Background() }
func (s *processRetryNoopSuite) StartTime() time.Time          { return time.Time{} }
func (s *processRetryNoopSuite) SetError(...ErrorOption)       {}
func (s *processRetryNoopSuite) SetTag(string, any)            {}
func (s *processRetryNoopSuite) GetTag(string) (any, bool)     { return nil, false }
func (s *processRetryNoopSuite) SuiteID() uint64               { return 0 }
func (s *processRetryNoopSuite) Module() TestModule            { return s.module }
func (s *processRetryNoopSuite) Name() string                  { return s.name }
func (s *processRetryNoopSuite) Close(...TestSuiteCloseOption) {}
func (s *processRetryNoopSuite) CreateTest(name string, _ ...TestStartOption) Test {
	return &processRetryNoopTest{suite: s, name: name}
}

type processRetryNoopTest struct {
	suite     TestSuite
	name      string
	startTime time.Time
}

func (t *processRetryNoopTest) Context() context.Context                   { return context.Background() }
func (t *processRetryNoopTest) StartTime() time.Time                       { return t.startTime }
func (t *processRetryNoopTest) SetError(...ErrorOption)                    {}
func (t *processRetryNoopTest) SetTag(string, any)                         {}
func (t *processRetryNoopTest) GetTag(string) (any, bool)                  { return nil, false }
func (t *processRetryNoopTest) TestID() uint64                             { return 0 }
func (t *processRetryNoopTest) Name() string                               { return t.name }
func (t *processRetryNoopTest) Suite() TestSuite                           { return t.suite }
func (t *processRetryNoopTest) Close(TestResultStatus, ...TestCloseOption) {}
func (t *processRetryNoopTest) SetTestFunc(*runtime.Func)                  {}
func (t *processRetryNoopTest) SetBenchmarkData(string, map[string]any)    {}
func (t *processRetryNoopTest) Log(string, string)                         {}

// NewProcessRetryNoopTest returns a non-recording test hierarchy for retry
// children that need native helper behavior without CI Visibility ownership.
func NewProcessRetryNoopTest(name string, startTime time.Time) Test {
	session := &processRetryNoopSession{framework: "go"}
	module := &processRetryNoopModule{session: session, framework: "go"}
	suite := &processRetryNoopSuite{module: module}
	return &processRetryNoopTest{suite: suite, name: name, startTime: startTime}
}

type processRetryNoopMockTracer struct{}

func (t *processRetryNoopMockTracer) StartSpan(string, ...tracer.StartSpanOption) *tracer.Span {
	return nil
}
func (t *processRetryNoopMockTracer) Extract(any) (*tracer.SpanContext, error) { return nil, nil }
func (t *processRetryNoopMockTracer) Inject(*tracer.SpanContext, any) error    { return nil }
func (t *processRetryNoopMockTracer) TracerConf() tracer.TracerConf            { return tracer.TracerConf{} }
func (t *processRetryNoopMockTracer) Flush()                                   {}
func (t *processRetryNoopMockTracer) Stop()                                    {}
func (t *processRetryNoopMockTracer) OpenSpans() []*mocktracer.Span            { return nil }
func (t *processRetryNoopMockTracer) FinishSpan(*tracer.Span)                  {}
func (t *processRetryNoopMockTracer) FinishedSpans() []*mocktracer.Span        { return nil }
func (t *processRetryNoopMockTracer) SentDSMBacklogs() []mocktracer.DSMBacklog { return nil }
func (t *processRetryNoopMockTracer) Reset()                                   {}
