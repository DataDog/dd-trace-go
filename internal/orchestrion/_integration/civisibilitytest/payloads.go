// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package civisibilitytest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"

	"github.com/tinylib/msgp/msgp"

	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	ddenv "github.com/DataDog/dd-trace-go/v2/internal/env"
)

// MockServerOption configures the CI Visibility mock server.
type MockServerOption func(*mockServerConfig)

type envUpdate struct {
	key   string
	value string
	unset bool
}

const mockServerReadCacheRootEnv = "CIVISIBILITY_TEST_READ_CACHE_ROOT"

type mockServerConfig struct {
	settings       civisibilitynet.SettingsResponseData
	knownTests     *civisibilitynet.KnownTestsResponseData
	testManagement *civisibilitynet.TestManagementTestsResponseDataModules
	env            []envUpdate
}

// Payloads stores decoded CI Visibility test-cycle payloads received by the mock intake.
type Payloads struct {
	mu       sync.Mutex
	payloads []*Payload
}

// Payload is the decoded CI Visibility test-cycle payload shape used by fixtures.
type Payload struct {
	Version  int      `json:"version"`
	Metadata Metadata `json:"metadata"`
	Events   Events   `json:"events"`
}

// Metadata is the decoded metadata block included in CI Visibility payloads.
type Metadata struct {
	All            MetadataAll   `json:"*"`
	TestSessionEnd MetadataOther `json:"test_session_end"`
	TestModuleEnd  MetadataOther `json:"test_module_end"`
	TestSuiteEnd   MetadataOther `json:"test_suite_end"`
	Test           MetadataOther `json:"test"`
}

// MetadataAll is the decoded payload-wide metadata shape.
type MetadataAll struct {
	Language       string `json:"language"`
	LibraryVersion string `json:"library_version"`
	RuntimeID      string `json:"runtime-id"`
}

// MetadataOther is the decoded per-event metadata shape.
type MetadataOther struct {
	TestSessionName string `json:"test_session.name"`
}

// Events is a collection of decoded CI Visibility events with assertion helpers.
type Events []Event

// Event is a decoded CI Visibility event.
type Event struct {
	Type    string  `json:"type"`
	Version int     `json:"version"`
	Content Content `json:"content"`
}

// Content is the decoded span content for a CI Visibility event.
type Content struct {
	TestSessionID uint64             `json:"test_session_id"`
	TestModuleID  uint64             `json:"test_module_id"`
	TestSuiteID   uint64             `json:"test_suite_id"`
	SpanID        uint64             `json:"span_id"`
	TraceID       uint64             `json:"trace_id"`
	Name          string             `json:"name"`
	Service       string             `json:"service"`
	Resource      string             `json:"resource"`
	Type          string             `json:"type"`
	Start         uint64             `json:"start"`
	Duration      uint               `json:"duration"`
	Error         int                `json:"error"`
	Meta          map[string]string  `json:"meta"`
	Metrics       map[string]float64 `json:"metrics"`
}

// WithSettings configures the settings endpoint response.
func WithSettings(settings civisibilitynet.SettingsResponseData) MockServerOption {
	return func(cfg *mockServerConfig) {
		cfg.settings = settings
	}
}

// WithKnownTests configures the known-tests endpoint response.
func WithKnownTests(knownTests civisibilitynet.KnownTestsResponseData) MockServerOption {
	return func(cfg *mockServerConfig) {
		cfg.knownTests = &knownTests
	}
}

// WithTestManagement configures the test-management endpoint response.
func WithTestManagement(testManagement civisibilitynet.TestManagementTestsResponseDataModules) MockServerOption {
	return func(cfg *mockServerConfig) {
		cfg.testManagement = &testManagement
	}
}

// WithEnv sets an environment variable while the mock server is active.
func WithEnv(key, value string) MockServerOption {
	return func(cfg *mockServerConfig) {
		cfg.env = append(cfg.env, envUpdate{key: key, value: value})
	}
}

// WithoutEnv unsets an environment variable while the mock server is active.
func WithoutEnv(key string) MockServerOption {
	return func(cfg *mockServerConfig) {
		cfg.env = append(cfg.env, envUpdate{key: key, unset: true})
	}
}

// StartMockServer starts an agentless CI Visibility intake mock and configures the process environment.
func StartMockServer(settings civisibilitynet.SettingsResponseData) (*httptest.Server, *Payloads, func()) {
	return StartMockServerWithOptions(WithSettings(settings))
}

// StartMockServerWithOptions starts an agentless CI Visibility intake mock configured with options.
func StartMockServerWithOptions(opts ...MockServerOption) (*httptest.Server, *Payloads, func()) {
	cfg := &mockServerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	payloads := &Payloads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		switch r.URL.Path {
		case "/api/v2/citestcycle":
			payloads.handleTestCycle(w, r)
		case "/api/v2/libraries/tests/services/setting":
			_ = drainRequestBody(r)
			writeSettingsResponse(w, cfg.settings)
		case "/api/v2/ci/libraries/tests":
			_ = drainRequestBody(r)
			writeKnownTestsResponse(w, cfg.knownTests)
		case "/api/v2/test/libraries/test-management/tests":
			_ = drainRequestBody(r)
			writeTestManagementResponse(w, cfg.testManagement)
		case "/api/v2/git/repository/search_commits":
			_ = drainRequestBody(r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		case "/api/v2/git/repository/packfile", "/api/v2/logs":
			_ = drainRequestBody(r)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))

	// httptest ports can be reused between scenario subprocesses, so keep mock
	// CI Visibility responses out of the shared short-lived read cache.
	cacheRoot, cleanupReadCache := isolateReadCacheForMockServer("")
	envUpdates := []envUpdate{
		{key: "DD_CIVISIBILITY_ENABLED", value: "true"},
		{key: "DD_CIVISIBILITY_AGENTLESS_ENABLED", value: "true"},
		{key: "DD_CIVISIBILITY_AGENTLESS_URL", value: server.URL},
		{key: "DD_API_KEY", value: "***"},
		{key: "DD_GIT_REPOSITORY_URL", value: "https://github.com/DataDog/dd-trace-go.git"},
		{key: "DD_GIT_COMMIT_SHA", value: "1234567890abcdef1234567890abcdef12345678"},
		{key: "DD_GIT_BRANCH", value: "main"},
	}
	if cacheRoot != "" {
		envUpdates = append(envUpdates, envUpdate{key: mockServerReadCacheRootEnv, value: cacheRoot})
	}

	restore := applyEnvUpdates(append(envUpdates, cfg.env...))

	return server, payloads, func() {
		restore()
		server.Close()
		cleanupReadCache()
	}
}

// ConfigureMockServerReadCacheFromEnv installs the mock server read cache root
// propagated to subprocesses. This is needed when the mock server runs in a
// parent process but CI Visibility initializes in a child process.
func ConfigureMockServerReadCacheFromEnv() func() {
	cacheRoot := os.Getenv(mockServerReadCacheRootEnv)
	if cacheRoot == "" {
		return func() {}
	}
	_, cleanupReadCache := isolateReadCacheForMockServer(cacheRoot)
	return cleanupReadCache
}

func isolateReadCacheForMockServer(cacheRoot string) (string, func()) {
	removeOnCleanup := false
	if cacheRoot == "" {
		var err error
		cacheRoot, err = os.MkdirTemp("", "dd-trace-go-civisibility-read-cache-*")
		if err != nil {
			log.Printf("unable to isolate CI Visibility read cache for mock server: %s", err)
			return "", func() {}
		}
		removeOnCleanup = true
	}

	civisibilitynet.SetReadCacheHooksForTesting(cacheRoot, nil, nil, nil, nil)
	return cacheRoot, func() {
		civisibilitynet.ResetReadCacheHooksForTesting()
		if removeOnCleanup {
			_ = os.RemoveAll(cacheRoot)
		}
	}
}

func writeSettingsResponse(w http.ResponseWriter, settings civisibilitynet.SettingsResponseData) {
	w.Header().Set("Content-Type", "application/json")
	response := struct {
		Data struct {
			ID         string                               `json:"id"`
			Type       string                               `json:"type"`
			Attributes civisibilitynet.SettingsResponseData `json:"attributes"`
		} `json:"data"`
	}{}
	response.Data.ID = "settings"
	response.Data.Type = "ci_app_libraries_tests_settings"
	response.Data.Attributes = settings
	_ = json.NewEncoder(w).Encode(response)
}

func writeKnownTestsResponse(w http.ResponseWriter, knownTests *civisibilitynet.KnownTestsResponseData) {
	w.Header().Set("Content-Type", "application/json")
	response := struct {
		Data struct {
			ID         string                                 `json:"id"`
			Type       string                                 `json:"type"`
			Attributes civisibilitynet.KnownTestsResponseData `json:"attributes"`
		} `json:"data"`
	}{}
	response.Data.ID = "known-tests"
	response.Data.Type = "ci_app_libraries_tests_request"
	if knownTests != nil {
		response.Data.Attributes = *knownTests
	}
	_ = json.NewEncoder(w).Encode(response)
}

func writeTestManagementResponse(w http.ResponseWriter, testManagement *civisibilitynet.TestManagementTestsResponseDataModules) {
	w.Header().Set("Content-Type", "application/json")
	response := struct {
		Data struct {
			ID         string                                                 `json:"id"`
			Type       string                                                 `json:"type"`
			Attributes civisibilitynet.TestManagementTestsResponseDataModules `json:"attributes"`
		} `json:"data"`
	}{}
	response.Data.ID = "test-management"
	response.Data.Type = "ci_app_libraries_tests_request"
	if testManagement != nil {
		response.Data.Attributes = *testManagement
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (p *Payloads) handleTestCycle(w http.ResponseWriter, r *http.Request) {
	body, err := decodeTestCycleBody(r.Body)
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var payload Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	p.mu.Lock()
	p.payloads = append(p.payloads, &payload)
	p.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func decodeTestCycleBody(body io.Reader) ([]byte, error) {
	gzipReader, err := gzip.NewReader(body)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	var jsonBuf bytes.Buffer
	if _, err := msgp.CopyToJSON(&jsonBuf, gzipReader); err != nil {
		return nil, err
	}
	return jsonBuf.Bytes(), nil
}

func drainRequestBody(r *http.Request) error {
	if r.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			return err
		}
		defer gzipReader.Close()
		_, err = io.Copy(io.Discard, gzipReader)
		return err
	}
	_, err := io.Copy(io.Discard, r.Body)
	return err
}

// Events returns all decoded events received so far.
func (p *Payloads) Events() Events {
	p.mu.Lock()
	defer p.mu.Unlock()
	var events Events
	for _, payload := range p.payloads {
		events = append(events, payload.Events...)
	}
	return events
}

// PayloadCount returns the number of CI Visibility test-cycle payloads received.
func (p *Payloads) PayloadCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.payloads)
}

// CheckPayloadCountAtLeast panics if fewer than count payloads were received.
func (p *Payloads) CheckPayloadCountAtLeast(count int) *Payloads {
	actual := p.PayloadCount()
	if actual < count {
		panic(fmt.Sprintf("expected at least %d payload(s), got %d", count, actual))
	}
	return p
}

// CheckEventsByType returns events with the given type and panics if the count does not match.
func (e Events) CheckEventsByType(eventType string, count int) Events {
	var result Events
	for _, event := range e {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	if len(result) != count {
		e.ShowEvents()
		panic(fmt.Sprintf("expected exactly %d event(s) with type %s, got %d", count, eventType, len(result)))
	}
	return result
}

// CheckEventsByResourceName returns events with the given resource and panics if the count does not match.
func (e Events) CheckEventsByResourceName(resourceName string, count int) Events {
	var result Events
	for _, event := range e {
		if event.Content.Resource == resourceName {
			result = append(result, event)
		}
	}
	if len(result) != count {
		e.ShowResourceNames()
		panic(fmt.Sprintf("expected exactly %d event(s) with resource %s, got %d", count, resourceName, len(result)))
	}
	return result
}

// CheckEventsByTagAndValue returns events with the given meta tag value and panics if the count does not match.
func (e Events) CheckEventsByTagAndValue(tagName, tagValue string, count int) Events {
	var result Events
	for _, event := range e {
		if value, ok := event.Content.Meta[tagName]; ok && value == tagValue {
			result = append(result, event)
		}
	}
	if len(result) != count {
		e.ShowEvents()
		panic(fmt.Sprintf("expected exactly %d event(s) with tag %s=%s, got %d", count, tagName, tagValue, len(result)))
	}
	return result
}

// CheckEventsWithoutTag returns events without the given meta tag and panics if the count does not match.
func (e Events) CheckEventsWithoutTag(tagName string, count int) Events {
	var result Events
	for _, event := range e {
		if _, ok := event.Content.Meta[tagName]; !ok {
			result = append(result, event)
		}
	}
	if len(result) != count {
		e.ShowEvents()
		panic(fmt.Sprintf("expected exactly %d event(s) without tag %s, got %d", count, tagName, len(result)))
	}
	return result
}

// CheckEventsByMetricAndValue returns events with the given metric value and panics if the count does not match.
func (e Events) CheckEventsByMetricAndValue(metricName string, metricValue float64, count int) Events {
	var result Events
	for _, event := range e {
		if value, ok := event.Content.Metrics[metricName]; ok && value == metricValue {
			result = append(result, event)
		}
	}
	if len(result) != count {
		panic(fmt.Sprintf("expected exactly %d event(s) with metric %s=%v, got %d", count, metricName, metricValue, len(result)))
	}
	return result
}

// Except returns events whose resource does not appear in any provided event collection.
func (e Events) Except(groups ...Events) Events {
	var filtered Events
	for _, event := range e {
		contains := false
		for _, group := range groups {
			for _, candidate := range group {
				if event.Content.Resource == candidate.Content.Resource {
					contains = true
				}
			}
		}
		if !contains {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// HasCount panics if the number of events does not match count.
func (e Events) HasCount(count int) Events {
	if len(e) != count {
		e.ShowResourceNames()
		panic(fmt.Sprintf("expected exactly %d event(s), got %d", count, len(e)))
	}
	return e
}

// ShowResourceNames prints event resources to help diagnose failed assertions.
func (e Events) ShowResourceNames() Events {
	for i, event := range e {
		_, _ = fmt.Printf("  [%d] = type:%s resource:%s\n", i, event.Type, event.Content.Resource)
	}
	return e
}

// ShowEvents prints event type, resource, status, retry, and final-status fields for diagnostics.
func (e Events) ShowEvents() Events {
	for i, event := range e {
		_, _ = fmt.Printf(
			"  [%d] = type:%s resource:%s status:%s retry:%s reason:%s final:%s\n",
			i,
			event.Type,
			event.Content.Resource,
			event.Content.Meta["test.status"],
			event.Content.Meta["test.is_retry"],
			event.Content.Meta["test.retry_reason"],
			event.Content.Meta["test.final_status"],
		)
	}
	return e
}

type envSnapshot struct {
	key   string
	value string
	had   bool
}

func applyEnvUpdates(updates []envUpdate) func() {
	snapshots := make([]envSnapshot, 0, len(updates))
	for _, update := range updates {
		old, had := ddenv.Lookup(update.key)
		snapshots = append(snapshots, envSnapshot{key: update.key, value: old, had: had})
		if update.unset {
			_ = os.Unsetenv(update.key)
			continue
		}
		_ = os.Setenv(update.key, update.value)
	}
	return func() {
		for i := len(snapshots) - 1; i >= 0; i-- {
			if snapshots[i].had {
				_ = os.Setenv(snapshots[i].key, snapshots[i].value)
			} else {
				_ = os.Unsetenv(snapshots[i].key)
			}
		}
	}
}
