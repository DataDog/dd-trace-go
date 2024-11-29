// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
)

func TestNewCoverageWriter(t *testing.T) {
	writer := newCoverageWriter()
	assert.NotNil(t, writer)
	assert.NotNil(t, writer.client)
	assert.NotNil(t, writer.payload)
	assert.NotNil(t, writer.climit)
}

func TestCoverageWriterAdd(t *testing.T) {
	writer := newCoverageWriter()
	coverage := &testCoverage{}
	writer.add(coverage)
	assert.Equal(t, writer.payload.itemCount(), 1)
}

func TestCoverageWriterStop(t *testing.T) {
	writer := newCoverageWriter()
	coverage := &testCoverage{}
	writer.add(coverage)
	writer.stop()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestCoverageWriterFlush(t *testing.T) {
	writer := newCoverageWriter()
	coverage := &testCoverage{}
	writer.add(coverage)
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestCoverageWriterConcurrentFlush(t *testing.T) {
	writer := newCoverageWriter()
	coverage := &testCoverage{}

	for i := 0; i < concurrentConnectionLimit+1; i++ {
		writer.add(coverage)
	}
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestCoverageWriterFlushError(t *testing.T) {
	writer := newCoverageWriter()
	writer.client = &MockClient{SendCoveragePayloadFunc: func(_ io.Reader) error {
		return fmt.Errorf("mock error")
	},
	}
	coverage := &testCoverage{}
	writer.add(coverage)
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

// MockClient is a mock implementation of the Client interface for testing purposes.
type MockClient struct {
	SendCoveragePayloadFunc        func(ciTestCovPayload io.Reader) error
	GetSettingsFunc                func() (*net.SettingsResponseData, error)
	GetEarlyFlakeDetectionDataFunc func() (*net.EfdResponseData, error)
	GetCommitsFunc                 func(localCommits []string) ([]string, error)
	SendPackFilesFunc              func(commitSha string, packFiles []string) (bytes int64, err error)
	GetSkippableTestsFunc          func() (correlationId string, skippables map[string]map[string][]net.SkippableResponseDataAttributes, err error)
}

func (m *MockClient) SendCoveragePayload(ciTestCovPayload io.Reader) error {
	return m.SendCoveragePayloadFunc(ciTestCovPayload)
}

func (m *MockClient) GetSettings() (*net.SettingsResponseData, error) {
	return m.GetSettingsFunc()
}

func (m *MockClient) GetEarlyFlakeDetectionData() (*net.EfdResponseData, error) {
	return m.GetEarlyFlakeDetectionDataFunc()
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
