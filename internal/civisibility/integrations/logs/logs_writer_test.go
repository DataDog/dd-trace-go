// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
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
	assert.True(t, writer.add(entry))
	assert.Equal(t, writer.payload.itemCount(), 1)
}

func TestLogsWriterStop(t *testing.T) {
	writer := newLogsWriter()
	writer.client = &MockClient{SendLogsFunc: drainLogsPayload}
	entry := &logEntry{}
	writer.add(entry)
	writer.stop()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestLogsWriterFlush(t *testing.T) {
	writer := newLogsWriter()
	writer.client = &MockClient{SendLogsFunc: drainLogsPayload}
	entry := &logEntry{}
	writer.add(entry)
	writer.flush()
	assert.Equal(t, 0, writer.payload.itemCount())
}

func TestLogsWriterConcurrentFlush(t *testing.T) {
	writer := newLogsWriter()
	writer.client = &MockClient{SendLogsFunc: drainLogsPayload}
	entry := &logEntry{}

	for range concurrentConnectionLimit + 1 {
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

func TestLogsWriterStopFlushesAcceptedLogs(t *testing.T) {
	writer := newLogsWriter()
	recorder := newLogSendRecorder(nil)
	writer.client = &MockClient{SendLogsFunc: recorder.send}

	expected := map[string]int{}
	for i := range 32 {
		message := fmt.Sprintf("message-%d", i)
		if writer.add(&logEntry{Message: message}) {
			expected[message]++
		}
	}

	writer.stop()

	assert.Equal(t, 0, writer.payload.itemCount())
	assert.Equal(t, expected, recorder.messages())
}

func TestLogsWriterConcurrentAddFlushStopRace(t *testing.T) {
	writer := newLogsWriter()
	recorder := newLogSendRecorder(nil)
	writer.client = &MockClient{SendLogsFunc: recorder.send}

	acceptedMu := sync.Mutex{}
	accepted := map[string]int{}
	var wg sync.WaitGroup
	for i := range 128 {
		message := fmt.Sprintf("concurrent-%d", i)
		wg.Go(func() {
			if writer.add(&logEntry{Message: message}) {
				acceptedMu.Lock()
				accepted[message]++
				acceptedMu.Unlock()
			}
		})
		wg.Go(func() {
			writer.flush()
		})
	}
	wg.Go(func() {
		writer.stop()
	})
	wg.Wait()
	writer.stop()

	acceptedMu.Lock()
	defer acceptedMu.Unlock()
	assert.Equal(t, accepted, recorder.messages())
}

func TestLogsWriterAddAfterStopIsIgnored(t *testing.T) {
	writer := newLogsWriter()
	recorder := newLogSendRecorder(nil)
	writer.client = &MockClient{SendLogsFunc: recorder.send}

	writer.stop()

	assert.False(t, writer.add(&logEntry{Message: "late"}))
	assert.Equal(t, 0, writer.payload.itemCount())
	assert.Empty(t, recorder.messages())
}

func TestLogsWriterDoesNotBlockOnConnectionLimitWhileSchedulingFlush(t *testing.T) {
	writer := newLogsWriter()
	release := make(chan struct{})
	started := make(chan struct{}, concurrentConnectionLimit+1)
	writer.client = &MockClient{SendLogsFunc: func(payload io.Reader) error {
		started <- struct{}{}
		<-release
		return drainLogsPayload(payload)
	}}

	done := make(chan struct{})
	errCh := make(chan string, 1)
	go func() {
		defer close(done)
		for i := range concurrentConnectionLimit + 1 {
			if !writer.add(&logEntry{Message: fmt.Sprintf("blocked-%d", i)}) {
				errCh <- "writer rejected a log entry before stop"
				return
			}
			writer.flush()
		}
	}()

	select {
	case <-done:
		select {
		case msg := <-errCh:
			close(release)
			t.Fatal(msg)
		default:
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("flush scheduling blocked while uploads were waiting")
	}

	for range concurrentConnectionLimit {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("expected uploads to start")
		}
	}

	close(release)
	writer.stop()
}

func drainLogsPayload(payload io.Reader) error {
	_, err := io.Copy(io.Discard, payload)
	return err
}

type logSendRecorder struct {
	mu             sync.Mutex
	release        <-chan struct{}
	messagesByText map[string]int
}

func newLogSendRecorder(release <-chan struct{}) *logSendRecorder {
	return &logSendRecorder{
		release:        release,
		messagesByText: map[string]int{},
	}
}

func (r *logSendRecorder) send(payload io.Reader) error {
	if r.release != nil {
		<-r.release
	}
	raw, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	var entries logsEntriesPayload
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		r.messagesByText[entry.Message]++
	}
	return nil
}

func (r *logSendRecorder) messages() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.messagesByText))
	maps.Copy(out, r.messagesByText)
	return out
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
