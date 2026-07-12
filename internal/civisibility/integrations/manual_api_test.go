// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/impactedtests"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	internalenv "github.com/DataDog/dd-trace-go/v2/internal/env"
)

// Mocking the ddTslvEvent interface
type MockDdTslvEvent struct {
	mock.Mock
}

func TestTryPushCiVisibilityPreCloseActionRejectsShutdown(t *testing.T) {
	oldState := civisibility.GetState()
	closeActionsMutex.Lock()
	oldPreCloseActions := preCloseActions
	preCloseActions = nil
	closeActionsMutex.Unlock()
	t.Cleanup(func() {
		civisibility.SetState(oldState)
		closeActionsMutex.Lock()
		preCloseActions = oldPreCloseActions
		closeActionsMutex.Unlock()
	})

	civisibility.SetState(civisibility.StateInitialized)
	require.True(t, TryPushCiVisibilityPreCloseAction(func() {}))
	closeActionsMutex.Lock()
	require.Len(t, preCloseActions, 1)
	closeActionsMutex.Unlock()

	civisibility.SetState(civisibility.StateExiting)
	require.False(t, TryPushCiVisibilityPreCloseAction(func() {}))
	closeActionsMutex.Lock()
	require.Len(t, preCloseActions, 1)
	closeActionsMutex.Unlock()
}

func TestCIVisibilityPreCloseActionsRunUnlockedBeforeCloseActions(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)

	var order []string
	require.True(t, TryPushCiVisibilityPreCloseAction(func() {
		order = append(order, "pre-close")
		registered := make(chan struct{})
		go func() {
			PushCiVisibilityCloseAction(func() {
				order = append(order, "late close")
			})
			close(registered)
		}()
		select {
		case <-registered:
		case <-time.After(time.Second):
			t.Fatal("pre-close action ran while closeActionsMutex was held")
		}
	}))
	PushCiVisibilityCloseAction(func() {
		order = append(order, "close")
	})

	civisibility.SetState(civisibility.StateInitialized)
	ExitCiVisibility()

	require.Equal(t, []string{"pre-close", "late close", "close"}, order)
}

func (m *MockDdTslvEvent) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

func (m *MockDdTslvEvent) StartTime() time.Time {
	args := m.Called()
	return args.Get(0).(time.Time)
}

func (m *MockDdTslvEvent) SetError(options ...ErrorOption) {
	m.Called(options)
}

func (m *MockDdTslvEvent) SetTag(key string, value any) {
	m.Called(key, value)
}

func (m *MockDdTslvEvent) GetTag(key string) (any, bool) {
	args := m.Called(key)
	return args.Get(0), true
}

// Mocking the DdTest interface
type MockDdTest struct {
	MockDdTslvEvent
	mock.Mock
}

func (m *MockDdTest) TestID() uint64 {
	args := m.Called()
	return args.Get(0).(uint64)
}

func (m *MockDdTest) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTest) Suite() TestSuite {
	args := m.Called()
	return args.Get(0).(TestSuite)
}

func (m *MockDdTest) Close(status TestResultStatus, options ...TestCloseOption) {
	m.Called(status, options)
}

func (m *MockDdTest) SetTestFunc(fn *runtime.Func) {
	m.Called(fn)
}

func (m *MockDdTest) SetBenchmarkData(measureType string, data map[string]any) {
	m.Called(measureType, data)
}

func (m *MockDdTest) Log(message string, tags string) {
	m.Called(message, tags)
}

// Mocking the DdTestSession interface
type MockDdTestSession struct {
	MockDdTslvEvent
	mock.Mock
}

func (m *MockDdTestSession) SessionID() uint64 {
	args := m.Called()
	return args.Get(0).(uint64)
}

func (m *MockDdTestSession) Command() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestSession) Framework() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestSession) WorkingDirectory() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestSession) Close(exitCode int, options ...TestSessionCloseOption) {
	m.Called(exitCode, options)
}

func (m *MockDdTestSession) GetOrCreateModule(name string, options ...TestModuleStartOption) TestModule {
	args := m.Called(name, options)
	return args.Get(0).(TestModule)
}

// Mocking the DdTestModule interface
type MockDdTestModule struct {
	MockDdTslvEvent
	mock.Mock
}

func (m *MockDdTestModule) ModuleID() uint64 {
	args := m.Called()
	return args.Get(0).(uint64)
}

func (m *MockDdTestModule) Session() TestSession {
	args := m.Called()
	return args.Get(0).(TestSession)
}

func (m *MockDdTestModule) Framework() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestModule) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestModule) Close(options ...TestModuleCloseOption) {
	m.Called(options)
}

func (m *MockDdTestModule) GetOrCreateSuite(name string, options ...TestSuiteStartOption) TestSuite {
	args := m.Called(name, options)
	return args.Get(0).(TestSuite)
}

func (m *MockDdTestModule) GetOrCreateSuiteWithStartTime(name string, startTime time.Time) TestSuite {
	args := m.Called(name, startTime)
	return args.Get(0).(TestSuite)
}

// Mocking the DdTestSuite interface
type MockDdTestSuite struct {
	MockDdTslvEvent
	mock.Mock
}

func (m *MockDdTestSuite) SuiteID() uint64 {
	args := m.Called()
	return args.Get(0).(uint64)
}

func (m *MockDdTestSuite) Module() TestModule {
	args := m.Called()
	return args.Get(0).(TestModule)
}

func (m *MockDdTestSuite) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestSuite) Close(options ...TestSuiteCloseOption) {
	m.Called(options)
}

func TestProcessRetryChildManualAPIsAreNoop(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	t.Setenv(constants.CIVisibilityInternalRetryProcessChild, "true")
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "child-sentinel")
	t.Setenv("DD_TRACE_SAMPLE_RATE", "sample-sentinel")

	var clientCalls atomic.Int32
	var uploadCalls atomic.Int32
	var searchCalls atomic.Int32
	var tracerInitializationCalls atomic.Int32
	newCIVisibilityClientWithServiceNameFunc = func(string) civisibilitynet.Client {
		clientCalls.Add(1)
		return nil
	}
	uploadRepositoryChangesFunc = func() (int64, error) {
		uploadCalls.Add(1)
		return 0, nil
	}
	getSearchCommitsFunc = func() (*searchCommitsResponse, error) {
		searchCalls.Add(1)
		return newSearchCommitsResponse(nil, nil, false), nil
	}

	internalCiVisibilityInitialization(func([]tracer.StartOption) {
		tracerInitializationCalls.Add(1)
	})
	EnsureCiVisibilityInitialization()

	session := CreateTestSession(WithTestSessionCommand("cmd"), WithTestSessionWorkingDirectory("wd"))
	require.NotNil(t, session)
	require.Zero(t, session.SessionID())
	require.Equal(t, "cmd", session.Command())
	require.Equal(t, "wd", session.WorkingDirectory())
	require.Equal(t, context.Background(), session.Context())

	module := session.GetOrCreateModule("module")
	require.NotNil(t, module)
	require.Zero(t, module.ModuleID())
	require.Equal(t, "module", module.Name())
	require.Equal(t, session, module.Session())
	require.Equal(t, context.Background(), module.Context())

	suite := module.GetOrCreateSuite("suite")
	require.NotNil(t, suite)
	require.Zero(t, suite.SuiteID())
	require.Equal(t, "suite", suite.Name())
	require.Equal(t, module, suite.Module())
	require.Equal(t, context.Background(), suite.Context())

	test := suite.CreateTest("test")
	require.NotNil(t, test)
	require.Zero(t, test.TestID())
	require.Equal(t, "test", test.Name())
	require.Equal(t, suite, test.Suite())
	require.Equal(t, context.Background(), test.Context())

	session.SetTag("tag", "value")
	session.SetError(WithErrorInfo("type", "message", "stack"))
	module.SetTag("tag", "value")
	module.SetError(WithErrorInfo("type", "message", "stack"))
	suite.SetTag("tag", "value")
	suite.SetError(WithErrorInfo("type", "message", "stack"))
	test.SetTag("tag", "value")
	test.SetError(WithErrorInfo("type", "message", "stack"))
	test.SetBenchmarkData("duration", map[string]any{"run": 1})
	test.SetTestFunc(nil)
	test.Log("message", "tag:value")
	test.Close(ResultStatusPass)
	suite.Close()
	module.Close()
	session.Close(0)
	ExitCiVisibility()

	require.NotNil(t, GetSettings())
	require.NotNil(t, GetKnownTests())
	require.NotNil(t, GetTestManagementTestsData())
	require.NotNil(t, GetFlakyRetriesSettings())
	require.Nil(t, GetSkippableTests())
	require.Nil(t, GetSkippableTestsResponse())
	require.Nil(t, GetImpactedTestsAnalyzer())
	mockTracer := InitializeCIVisibilityMock()
	require.NotNil(t, mockTracer)
	require.Nil(t, mockTracer.StartSpan("child"))

	require.Zero(t, clientCalls.Load())
	require.Zero(t, uploadCalls.Load())
	require.Zero(t, searchCalls.Load())
	require.Zero(t, tracerInitializationCalls.Load())
	require.Equal(t, civisibility.StateUninitialized, civisibility.GetState())
	require.Nil(t, ciVisibilityClient)
	require.Nil(t, mTracer)
	require.Empty(t, closeActions)
	require.Nil(t, currentCIVisibilitySignalHandlerForTesting())
	ciVisibilityEnabled, ok := internalenv.Lookup(constants.CIVisibilityEnabledEnvironmentVariable)
	require.True(t, ok)
	require.Equal(t, "child-sentinel", ciVisibilityEnabled)
	sampleRate, ok := internalenv.Lookup("DD_TRACE_SAMPLE_RATE")
	require.True(t, ok)
	require.Equal(t, "sample-sentinel", sampleRate)
}

func TestProcessRetryChildTransportKeyAllowlist(t *testing.T) {
	for _, key := range []string{
		constants.CIVisibilityInternalRetryProcessChild,
		constants.CIVisibilityInternalRetryProcessResultPath,
		constants.CIVisibilityInternalRetryProcessTestName,
		constants.CIVisibilityInternalRetryProcessAttempt,
		constants.CIVisibilityInternalRetryProcessReason,
	} {
		require.True(t, IsProcessRetryChildTransportKey(key))
	}
	require.False(t, IsProcessRetryChildTransportKey("DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_UNKNOWN"))
}

func TestProcessRetryChildFeatureGettersHideCachedParentState(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(resetCIVisibilityBootstrapStateForTesting)

	ciVisibilitySkippables = map[string]map[string][]civisibilitynet.SkippableResponseDataAttributes{"parent": {}}
	ciVisibilitySkippablesResponse = &civisibilitynet.SkippableTestsResponse{}
	ciVisibilityImpactedTestsAnalyzer = &impactedtests.ImpactedTestAnalyzer{}
	t.Setenv(constants.CIVisibilityInternalRetryProcessChild, "true")

	require.NotSame(t, &ciVisibilitySettings, GetSettings())
	require.NotSame(t, &ciVisibilityKnownTests, GetKnownTests())
	require.NotSame(t, &ciVisibilityTestManagementTests, GetTestManagementTestsData())
	require.NotSame(t, &ciVisibilityFlakyRetriesSettings, GetFlakyRetriesSettings())
	require.Nil(t, GetSkippableTests())
	require.Nil(t, GetSkippableTestsResponse())
	require.Nil(t, GetImpactedTestsAnalyzer())
}

func TestProcessRetryChildStartupHasNoCloseActions(t *testing.T) {
	if isProcessRetryChild() {
		require.Empty(t, closeActions)
		require.Equal(t, civisibility.StateUninitialized, civisibility.GetState())
		require.Zero(t, CreateTestSession().SessionID())
		require.Empty(t, closeActions)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryChildStartupHasNoCloseActions$", "-test.count=1")
	envWithoutChildMarker := make([]string, 0, len(os.Environ())+1)
	markerPrefix := strings.ToUpper(constants.CIVisibilityInternalRetryProcessChild) + "="
	for _, entry := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(entry), markerPrefix) {
			continue
		}
		envWithoutChildMarker = append(envWithoutChildMarker, entry)
	}
	cmd.Env = append(envWithoutChildMarker, constants.CIVisibilityInternalRetryProcessChild+"=true")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

func (m *MockDdTestSuite) CreateTest(name string, options ...TestStartOption) Test {
	args := m.Called(name, options)
	return args.Get(0).(Test)
}

// Unit tests
func TestDdTestSession(t *testing.T) {
	mockSession := new(MockDdTestSession)
	mockSession.On("Command").Return("test-command")
	mockSession.On("Framework").Return("test-framework")
	mockSession.On("WorkingDirectory").Return("/path/to/working/dir")
	mockSession.On("Close", 0, mock.Anything).Return()
	mockSession.On("GetOrCreateModule", "test-module", mock.Anything).Return(new(MockDdTestModule))

	session := (TestSession)(mockSession)
	assert.Equal(t, "test-command", session.Command())
	assert.Equal(t, "test-framework", session.Framework())
	assert.Equal(t, "/path/to/working/dir", session.WorkingDirectory())

	session.Close(0)
	mockSession.AssertCalled(t, "Close", 0, mock.Anything)

	now := time.Now()
	session.Close(0, WithTestSessionFinishTime(now))
	mockSession.AssertCalled(t, "Close", 0, mock.Anything)

	module := session.GetOrCreateModule("test-module")
	assert.NotNil(t, module)
	mockSession.AssertCalled(t, "GetOrCreateModule", "test-module", mock.Anything)

	module = session.GetOrCreateModule("test-module", WithTestModuleFramework("test-framework", "1.0"))
	assert.NotNil(t, module)
	mockSession.AssertCalled(t, "GetOrCreateModule", "test-module", mock.Anything)

	module = session.GetOrCreateModule("test-module", WithTestModuleFramework("test-framework", "1.0"), WithTestModuleStartTime(now))
	assert.NotNil(t, module)
	mockSession.AssertCalled(t, "GetOrCreateModule", "test-module", mock.Anything)
}

func TestDdTestModule(t *testing.T) {
	mockModule := new(MockDdTestModule)
	mockModule.On("Session").Return(new(MockDdTestSession))
	mockModule.On("Framework").Return("test-framework")
	mockModule.On("Name").Return("test-module")
	mockModule.On("Close", mock.Anything).Return()
	mockModule.On("GetOrCreateSuite", "test-suite", mock.Anything).Return(new(MockDdTestSuite))

	module := (TestModule)(mockModule)

	assert.Equal(t, "test-framework", module.Framework())
	assert.Equal(t, "test-module", module.Name())

	module.Close()
	mockModule.AssertCalled(t, "Close", mock.Anything)

	now := time.Now()
	module.Close(WithTestModuleFinishTime(now))
	mockModule.AssertCalled(t, "Close", mock.Anything)

	suite := module.GetOrCreateSuite("test-suite")
	assert.NotNil(t, suite)
	mockModule.AssertCalled(t, "GetOrCreateSuite", "test-suite", mock.Anything)

	suite = module.GetOrCreateSuite("test-suite", WithTestSuiteStartTime(now))
	assert.NotNil(t, suite)
	mockModule.AssertCalled(t, "GetOrCreateSuite", "test-suite", mock.Anything)
}

func TestDdTestSuite(t *testing.T) {
	mockSuite := new(MockDdTestSuite)
	mockSuite.On("Module").Return(new(MockDdTestModule))
	mockSuite.On("Name").Return("test-suite")
	mockSuite.On("Close", mock.Anything).Return()
	mockSuite.On("CreateTest", "test-name", mock.Anything).Return(new(MockDdTest))

	suite := (TestSuite)(mockSuite)

	assert.Equal(t, "test-suite", suite.Name())

	suite.Close()
	mockSuite.AssertCalled(t, "Close", mock.Anything)

	now := time.Now()
	suite.Close(WithTestSuiteFinishTime(now))
	mockSuite.AssertCalled(t, "Close", mock.Anything)

	test := suite.CreateTest("test-name")
	assert.NotNil(t, test)
	mockSuite.AssertCalled(t, "CreateTest", "test-name", mock.Anything)

	test = suite.CreateTest("test-name", WithTestStartTime(now))
	assert.NotNil(t, test)
	mockSuite.AssertCalled(t, "CreateTest", "test-name", mock.Anything)
}

func TestDdTest(t *testing.T) {
	mockTest := new(MockDdTest)
	mockTest.On("Name").Return("test-name")
	mockTest.On("Suite").Return(new(MockDdTestSuite))
	mockTest.On("Close", mock.Anything, mock.Anything).Return()
	mockTest.On("SetTestFunc", mock.Anything).Return()
	mockTest.On("SetBenchmarkData", "measure-type", mock.Anything).Return()

	test := (Test)(mockTest)

	assert.Equal(t, "test-name", test.Name())

	suite := test.Suite()
	assert.NotNil(t, suite)

	test.Close(ResultStatusPass)
	mockTest.AssertCalled(t, "Close", ResultStatusPass, mock.Anything)

	now := time.Now()
	test.Close(ResultStatusPass, WithTestFinishTime(now))
	mockTest.AssertCalled(t, "Close", ResultStatusPass, mock.Anything)

	skipReason := "SkipReason"
	test.Close(ResultStatusSkip, WithTestFinishTime(now), WithTestSkipReason(skipReason))
	mockTest.AssertCalled(t, "Close", ResultStatusSkip, mock.Anything)

	test.SetTestFunc(nil)
	mockTest.AssertCalled(t, "SetTestFunc", (*runtime.Func)(nil))

	benchmarkData := map[string]any{"key": "value"}
	test.SetBenchmarkData("measure-type", benchmarkData)
	mockTest.AssertCalled(t, "SetBenchmarkData", "measure-type", benchmarkData)
}
