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

func (m *MockDdTest) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTest) Suite() DdTestSuite {
	args := m.Called()
	return args.Get(0).(DdTestSuite)
}

func (m *MockDdTest) Close(status TestResultStatus) {
	m.Called(status)
}

func (m *MockDdTest) CloseWithFinishTime(status TestResultStatus, finishTime time.Time) {
	m.Called(status, finishTime)
}

func (m *MockDdTest) CloseWithFinishTimeAndSkipReason(status TestResultStatus, finishTime time.Time, skipReason string) {
	m.Called(status, finishTime, skipReason)
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

func (m *MockDdTestSession) Close(exitCode int) {
	m.Called(exitCode)
}

func (m *MockDdTestSession) CloseWithFinishTime(exitCode int, finishTime time.Time) {
	m.Called(exitCode, finishTime)
}

func (m *MockDdTestSession) GetOrCreateModule(name string) DdTestModule {
	args := m.Called(name)
	return args.Get(0).(DdTestModule)
}

func (m *MockDdTestSession) GetOrCreateModuleWithFramework(name string, framework string, frameworkVersion string) DdTestModule {
	args := m.Called(name, framework, frameworkVersion)
	return args.Get(0).(DdTestModule)
}

func (m *MockDdTestSession) GetOrCreateModuleWithFrameworkAndStartTime(name string, framework string, frameworkVersion string, startTime time.Time) DdTestModule {
	args := m.Called(name, framework, frameworkVersion, startTime)
	return args.Get(0).(DdTestModule)
}

// Mocking the DdTestModule interface
type MockDdTestModule struct {
	MockDdTslvEvent
	mock.Mock
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

func (m *MockDdTestModule) Close() {
	m.Called()
}

func (m *MockDdTestModule) CloseWithFinishTime(finishTime time.Time) {
	m.Called(finishTime)
}

func (m *MockDdTestModule) GetOrCreateSuite(name string) DdTestSuite {
	args := m.Called(name)
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

func (m *MockDdTestSuite) Module() DdTestModule {
	args := m.Called()
	return args.Get(0).(DdTestModule)
}

func (m *MockDdTestSuite) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDdTestSuite) Close() {
	m.Called()
}

func (m *MockDdTestSuite) CloseWithFinishTime(finishTime time.Time) {
	m.Called(finishTime)
}

func (m *MockDdTestSuite) CreateTest(name string) DdTest {
	args := m.Called(name)
	return args.Get(0).(DdTest)
}

func (m *MockDdTestSuite) CreateTestWithStartTime(name string, startTime time.Time) DdTest {
	args := m.Called(name, startTime)
	return args.Get(0).(DdTest)
}

// Unit tests
func TestDdTestSession(t *testing.T) {
	mockSession := new(MockDdTestSession)
	mockSession.On("Command").Return("test-command")
	mockSession.On("Framework").Return("test-framework")
	mockSession.On("WorkingDirectory").Return("/path/to/working/dir")
	mockSession.On("Close", 0).Return()
	mockSession.On("CloseWithFinishTime", 0, mock.Anything).Return()
	mockSession.On("GetOrCreateModule", "test-module").Return(new(MockDdTestModule))
	mockSession.On("GetOrCreateModuleWithFramework", "test-module", "test-framework", "1.0").Return(new(MockDdTestModule))
	mockSession.On("GetOrCreateModuleWithFrameworkAndStartTime", "test-module", "test-framework", "1.0", mock.Anything).Return(new(MockDdTestModule))

	session := (DdTestSession)(mockSession)
	assert.Equal(t, "test-command", session.Command())
	assert.Equal(t, "test-framework", session.Framework())
	assert.Equal(t, "/path/to/working/dir", session.WorkingDirectory())

	session.Close(0)
	mockSession.AssertCalled(t, "Close", 0)

	now := time.Now()
	session.CloseWithFinishTime(0, now)
	mockSession.AssertCalled(t, "CloseWithFinishTime", 0, now)

	module := session.GetOrCreateModule("test-module")
	assert.NotNil(t, module)
	mockSession.AssertCalled(t, "GetOrCreateModule", "test-module")

	module = session.GetOrCreateModuleWithFramework("test-module", "test-framework", "1.0")
	assert.NotNil(t, module)
	mockSession.AssertCalled(t, "GetOrCreateModuleWithFramework", "test-module", "test-framework", "1.0")

	module = session.GetOrCreateModuleWithFrameworkAndStartTime("test-module", "test-framework", "1.0", now)
	assert.NotNil(t, module)
	mockSession.AssertCalled(t, "GetOrCreateModuleWithFrameworkAndStartTime", "test-module", "test-framework", "1.0", now)
}

func TestDdTestModule(t *testing.T) {
	mockModule := new(MockDdTestModule)
	mockModule.On("Session").Return(new(MockDdTestSession))
	mockModule.On("Framework").Return("test-framework")
	mockModule.On("Name").Return("test-module")
	mockModule.On("Close").Return()
	mockModule.On("CloseWithFinishTime", mock.Anything).Return()
	mockModule.On("GetOrCreateSuite", "test-suite").Return(new(MockDdTestSuite))
	mockModule.On("GetOrCreateSuiteWithStartTime", "test-suite", mock.Anything).Return(new(MockDdTestSuite))

	module := (DdTestModule)(mockModule)

	assert.Equal(t, "test-framework", module.Framework())
	assert.Equal(t, "test-module", module.Name())

	module.Close()
	mockModule.AssertCalled(t, "Close")

	now := time.Now()
	module.CloseWithFinishTime(now)
	mockModule.AssertCalled(t, "CloseWithFinishTime", now)

	suite := module.GetOrCreateSuite("test-suite")
	assert.NotNil(t, suite)
	mockModule.AssertCalled(t, "GetOrCreateSuite", "test-suite")

	suite = module.GetOrCreateSuiteWithStartTime("test-suite", now)
	assert.NotNil(t, suite)
	mockModule.AssertCalled(t, "GetOrCreateSuiteWithStartTime", "test-suite", now)
}

func TestDdTestSuite(t *testing.T) {
	mockSuite := new(MockDdTestSuite)
	mockSuite.On("Module").Return(new(MockDdTestModule))
	mockSuite.On("Name").Return("test-suite")
	mockSuite.On("Close").Return()
	mockSuite.On("CloseWithFinishTime", mock.Anything).Return()
	mockSuite.On("CreateTest", "test-name").Return(new(MockDdTest))
	mockSuite.On("CreateTestWithStartTime", "test-name", mock.Anything).Return(new(MockDdTest))

	suite := (DdTestSuite)(mockSuite)

	assert.Equal(t, "test-suite", suite.Name())

	suite.Close()
	mockSuite.AssertCalled(t, "Close")

	now := time.Now()
	suite.CloseWithFinishTime(now)
	mockSuite.AssertCalled(t, "CloseWithFinishTime", now)

	test := suite.CreateTest("test-name")
	assert.NotNil(t, test)
	mockSuite.AssertCalled(t, "CreateTest", "test-name")

	test = suite.CreateTestWithStartTime("test-name", now)
	assert.NotNil(t, test)
	mockSuite.AssertCalled(t, "CreateTestWithStartTime", "test-name", now)
}

func TestDdTest(t *testing.T) {
	mockTest := new(MockDdTest)
	mockTest.On("Name").Return("test-name")
	mockTest.On("Suite").Return(new(MockDdTestSuite))
	mockTest.On("Close", ResultStatusPass).Return()
	mockTest.On("CloseWithFinishTime", ResultStatusPass, mock.Anything).Return()
	mockTest.On("CloseWithFinishTimeAndSkipReason", ResultStatusSkip, mock.Anything, "SkipReason").Return()
	mockTest.On("SetTestFunc", mock.Anything).Return()
	mockTest.On("SetBenchmarkData", "measure-type", mock.Anything).Return()

	test := (DdTest)(mockTest)

	assert.Equal(t, "test-name", test.Name())

	suite := test.Suite()
	assert.NotNil(t, suite)

	test.Close(ResultStatusPass)
	mockTest.AssertCalled(t, "Close", ResultStatusPass)

	now := time.Now()
	test.CloseWithFinishTime(ResultStatusPass, now)
	mockTest.AssertCalled(t, "CloseWithFinishTime", ResultStatusPass, now)

	skipReason := "SkipReason"
	test.CloseWithFinishTimeAndSkipReason(ResultStatusSkip, now, skipReason)
	mockTest.AssertCalled(t, "CloseWithFinishTimeAndSkipReason", ResultStatusSkip, now, skipReason)

	test.SetTestFunc(nil)
	mockTest.AssertCalled(t, "SetTestFunc", (*runtime.Func)(nil))

	benchmarkData := map[string]any{"key": "value"}
	test.SetBenchmarkData("measure-type", benchmarkData)
	mockTest.AssertCalled(t, "SetBenchmarkData", "measure-type", benchmarkData)
}
