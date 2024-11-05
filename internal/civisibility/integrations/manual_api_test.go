// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mocking the ddTslvEvent interface
type MockDdTslvEvent struct {
	mock.Mock
}

func (m *MockDdTslvEvent) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

func (m *MockDdTslvEvent) StartTime() time.Time {
	args := m.Called()
	return args.Get(0).(time.Time)
}

func (m *MockDdTslvEvent) SetError(err error) {
	m.Called(err)
}

func (m *MockDdTslvEvent) SetErrorInfo(errType string, message string, callstack string) {
	m.Called(errType, message, callstack)
}

func (m *MockDdTslvEvent) SetTag(key string, value interface{}) {
	m.Called(key, value)
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

func (m *MockDdTest) Suite() DdTestSuite {
	args := m.Called()
	return args.Get(0).(DdTestSuite)
}

func (m *MockDdTest) Close(status TestResultStatus, options ...DdTestCloseOption) {
	m.Called(status, options)
}

func (m *MockDdTest) SetTestFunc(fn *runtime.Func) {
	m.Called(fn)
}

func (m *MockDdTest) SetBenchmarkData(measureType string, data map[string]any) {
	m.Called(measureType, data)
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

func (m *MockDdTestSession) Close(exitCode int, options ...DdTestSessionCloseOption) {
	m.Called(exitCode, options)
}

func (m *MockDdTestSession) GetOrCreateModule(name string, options ...DdTestModuleStartOption) DdTestModule {
	args := m.Called(name, options)
	return args.Get(0).(DdTestModule)
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

func (m *MockDdTestModule) Session() DdTestSession {
	args := m.Called()
	return args.Get(0).(DdTestSession)
}

func (m *MockDdTestModule) Framework() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestModule) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestModule) Close(options ...DdTestModuleCloseOption) {
	m.Called(options)
}

func (m *MockDdTestModule) GetOrCreateSuite(name string, options ...DdTestSuiteStartOption) DdTestSuite {
	args := m.Called(name, options)
	return args.Get(0).(DdTestSuite)
}

func (m *MockDdTestModule) GetOrCreateSuiteWithStartTime(name string, startTime time.Time) DdTestSuite {
	args := m.Called(name, startTime)
	return args.Get(0).(DdTestSuite)
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

func (m *MockDdTestSuite) Module() DdTestModule {
	args := m.Called()
	return args.Get(0).(DdTestModule)
}

func (m *MockDdTestSuite) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestSuite) Close(options ...DdTestSuiteCloseOption) {
	m.Called(options)
}

func (m *MockDdTestSuite) CreateTest(name string, options ...DdTestStartOption) DdTest {
	args := m.Called(name, options)
	return args.Get(0).(DdTest)
}

// Unit tests
func TestDdTestSession(t *testing.T) {
	mockSession := new(MockDdTestSession)
	mockSession.On("Command").Return("test-command")
	mockSession.On("Framework").Return("test-framework")
	mockSession.On("WorkingDirectory").Return("/path/to/working/dir")
	mockSession.On("Close", 0, mock.Anything).Return()
	mockSession.On("GetOrCreateModule", "test-module", mock.Anything).Return(new(MockDdTestModule))

	session := (DdTestSession)(mockSession)
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

	module := (DdTestModule)(mockModule)

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

	suite := (DdTestSuite)(mockSuite)

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

	test := (DdTest)(mockTest)

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
