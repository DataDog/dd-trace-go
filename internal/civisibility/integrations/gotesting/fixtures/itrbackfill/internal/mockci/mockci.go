// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package mockci

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

// SkippableTest describes one backend ITR candidate returned by the mock.
type SkippableTest struct {
	Suite                   string
	Name                    string
	MissingLineCodeCoverage bool
}

// Server is a CI Visibility mock intake used by ITR backfill fixtures.
type Server struct {
	server *httptest.Server
	mu     sync.Mutex

	payloads         []Payload
	skippableRequest int
	coverageRequest  int
	restore          func()
}

// Payload is the decoded CI Visibility test-cycle payload shape used by fixtures.
type Payload struct {
	Events []Event `json:"events"`
}

// Event is one decoded CI Visibility event.
type Event struct {
	Type    string  `json:"type"`
	Content Content `json:"content"`
}

// Content is the span content used by fixture assertions.
type Content struct {
	Resource string             `json:"resource"`
	Meta     map[string]string  `json:"meta"`
	Metrics  map[string]float64 `json:"metrics"`
}

// Start starts the mock and configures process environment for agentless CI Visibility.
func Start(settings net.SettingsResponseData, tests []SkippableTest, coverage map[string][]byte) *Server {
	mock := &Server{}
	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		switch r.URL.Path {
		case "/api/v2/citestcycle":
			mock.handleTestCycle(w, r)
		case "/api/v2/libraries/tests/services/setting":
			_ = drain(r.Body)
			writeSettings(w, settings)
		case "/api/v2/ci/tests/skippable":
			_ = drain(r.Body)
			mock.handleSkippable(w, tests, coverage)
		case "/api/v2/citestcov":
			_ = drain(r.Body)
			mock.mu.Lock()
			mock.coverageRequest++
			mock.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		case "/api/v2/git/repository/search_commits":
			_ = drain(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		case "/api/v2/git/repository/packfile", "/api/v2/logs":
			_ = drain(r.Body)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))

	mock.restore = applyEnv(map[string]string{
		"DD_CIVISIBILITY_ENABLED":           "true",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED": "true",
		"DD_CIVISIBILITY_AGENTLESS_URL":     mock.server.URL,
		"DD_API_KEY":                        "***",
		"DD_GIT_REPOSITORY_URL":             "https://github.com/DataDog/dd-trace-go.git",
		"DD_GIT_COMMIT_SHA":                 "1234567890abcdef1234567890abcdef12345678",
		"DD_GIT_BRANCH":                     "main",
	})
	return mock
}

// Close restores environment variables and closes the mock server.
func (s *Server) Close() {
	if s.restore != nil {
		s.restore()
	}
	if s.server != nil {
		s.server.Close()
	}
}

// Events returns all decoded test-cycle events captured by the mock.
func (s *Server) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	var events []Event
	for _, payload := range s.payloads {
		events = append(events, payload.Events...)
	}
	return events
}

// SkippableRequests returns how many live skippable endpoint requests were received.
func (s *Server) SkippableRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skippableRequest
}

// CoverageRequests returns how many coverage intake requests were received.
func (s *Server) CoverageRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.coverageRequest
}

// HasEventMeta returns true when an event resource containing resourceContains
// has the requested meta tag value.
func (s *Server) HasEventMeta(resourceContains, key, value string) bool {
	for _, event := range s.Events() {
		if strings.Contains(event.Content.Resource, resourceContains) && event.Content.Meta[key] == value {
			return true
		}
	}
	return false
}

// SessionCoverage returns the session coverage metric when it was reported.
func (s *Server) SessionCoverage(metricName string) (float64, bool) {
	for _, event := range s.Events() {
		if event.Type != "test_session_end" {
			continue
		}
		if value, ok := event.Content.Metrics[metricName]; ok {
			return value, true
		}
	}
	return 0, false
}

// SessionMeta returns a test session meta tag when it was reported.
func (s *Server) SessionMeta(key string) (string, bool) {
	for _, event := range s.Events() {
		if event.Type != "test_session_end" {
			continue
		}
		if value, ok := event.Content.Meta[key]; ok {
			return value, true
		}
	}
	return "", false
}

func (s *Server) handleTestCycle(w http.ResponseWriter, r *http.Request) {
	gzipReader, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer gzipReader.Close()

	var jsonBuf bytes.Buffer
	if _, err := msgp.CopyToJSON(&jsonBuf, gzipReader); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var payload Payload
	if err := json.Unmarshal(jsonBuf.Bytes(), &payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.payloads = append(s.payloads, payload)
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleSkippable(w http.ResponseWriter, tests []SkippableTest, coverage map[string][]byte) {
	s.mu.Lock()
	s.skippableRequest++
	s.mu.Unlock()

	response := struct {
		Meta struct {
			CorrelationID string            `json:"correlation_id"`
			Coverage      map[string]string `json:"coverage,omitempty"`
		} `json:"meta"`
		Data []struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				Suite                   string `json:"suite"`
				Name                    string `json:"name"`
				MissingLineCodeCoverage bool   `json:"_missing_line_code_coverage,omitempty"`
			} `json:"attributes"`
		} `json:"data"`
	}{}
	response.Meta.CorrelationID = "itr-backfill-correlation"
	if coverage != nil {
		response.Meta.Coverage = make(map[string]string, len(coverage))
		for file, bitmap := range coverage {
			response.Meta.Coverage[file] = base64.StdEncoding.EncodeToString(bitmap)
		}
	}
	for _, test := range tests {
		item := struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Attributes struct {
				Suite                   string `json:"suite"`
				Name                    string `json:"name"`
				MissingLineCodeCoverage bool   `json:"_missing_line_code_coverage,omitempty"`
			} `json:"attributes"`
		}{ID: test.Name, Type: "test"}
		item.Attributes.Suite = test.Suite
		item.Attributes.Name = test.Name
		item.Attributes.MissingLineCodeCoverage = test.MissingLineCodeCoverage
		response.Data = append(response.Data, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func writeSettings(w http.ResponseWriter, settings net.SettingsResponseData) {
	response := struct {
		Data struct {
			ID         string                   `json:"id"`
			Type       string                   `json:"type"`
			Attributes net.SettingsResponseData `json:"attributes"`
		} `json:"data"`
	}{}
	response.Data.ID = "settings"
	response.Data.Type = "ci_app_libraries_tests_settings"
	response.Data.Attributes = settings
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func applyEnv(updates map[string]string) func() {
	previous := map[string]*string{}
	for key, value := range updates {
		if current, ok := env.Lookup(key); ok {
			copy := current
			previous[key] = &copy
		} else {
			previous[key] = nil
		}
		_ = os.Setenv(key, value)
	}
	return func() {
		for key, value := range previous {
			if value == nil {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, *value)
			}
		}
	}
}

func drain(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}
