// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"fmt"
	"io"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/stretchr/testify/assert"
)

func TestNewLogsWriter(t *testing.T) {
	writer := newLogsWriter()
	assert.NotNil(t, writer)
	assert.NotNil(t, writer.client)
	assert.NotNil(t, writer.payload)
	assert.NotNil(t, writer.climit)
}

func TestLogsWriterAdd(t *testing.T) {
	writer := newLogsWriter()
	entry := &logEntry{}
	writer.add(entry)
	assert.Equal(t, writer.payload.itemCount(), 1)
}

func TestLogsWriterStop(t *testing.T) {
	writer := newLogsWriter()
	entry := &logEntry{}
	writer.add(entry)
	writer.stop()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestLogsWriterFlush(t *testing.T) {
	writer := newLogsWriter()
	entry := &logEntry{}
	writer.add(entry)
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestLogsWriterConcurrentFlush(t *testing.T) {
	writer := newLogsWriter()
	entry := &logEntry{}

	for i := 0; i < concurrentConnectionLimit+1; i++ {
		writer.add(entry)
	}
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestLogsWriterFlushError(t *testing.T) {
	writer := newLogsWriter()
	writer.client = &MockClient{SendLogsFunc: func(_ io.Reader) error {
		return fmt.Errorf("mock error")
	},
	}
	entry := &logEntry{}
	writer.add(entry)
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

// MockClient is a mock implementation of the Client interface for testing purposes.
type MockClient struct {
	SendCoveragePayloadFunc           func(ciTestCovPayload io.Reader) error
	SendCoveragePayloadWithFormatFunc func(ciTestCovPayload io.Reader, format string) error
	GetSettingsFunc                   func() (*net.SettingsResponseData, error)
	GetKnownTestsFunc                 func() (*net.KnownTestsResponseData, error)
	GetCommitsFunc                    func(localCommits []string) ([]string, error)
	SendPackFilesFunc                 func(commitSha string, packFiles []string) (bytes int64, err error)
	GetSkippableTestsFunc             func() (correlationId string, skippables map[string]map[string][]net.SkippableResponseDataAttributes, err error)
	GetTestManagementTestsFunc        func() (*net.TestManagementTestsResponseDataModules, error)
	SendLogsFunc                      func(logsPayload io.Reader) error
}

func (m *MockClient) SendCoveragePayload(ciTestCovPayload io.Reader) error {
	return m.SendCoveragePayloadFunc(ciTestCovPayload)
}

func (m *MockClient) SendCoveragePayloadWithFormat(ciTestCovPayload io.Reader, format string) error {
	return m.SendCoveragePayloadWithFormatFunc(ciTestCovPayload, format)
}

func (m *MockClient) GetSettings() (*net.SettingsResponseData, error) {
	return m.GetSettingsFunc()
}

func (m *MockClient) GetKnownTests() (*net.KnownTestsResponseData, error) {
	return m.GetKnownTestsFunc()
}

func (m *MockClient) GetCommits(localCommits []string) ([]string, error) {
	return m.GetCommitsFunc(localCommits)
}

func (m *MockClient) SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error) {
	return m.SendPackFilesFunc(commitSha, packFiles)
}

func (m *MockClient) GetSkippableTests() (_ string, _ map[string]map[string][]net.SkippableResponseDataAttributes, err error) {
	return m.GetSkippableTestsFunc()
}

func (m *MockClient) GetTestManagementTests() (*net.TestManagementTestsResponseDataModules, error) {
	return m.GetTestManagementTestsFunc()
}

func (m *MockClient) SendLogs(logsPayload io.Reader) error {
	return m.SendLogsFunc(logsPayload)
}
