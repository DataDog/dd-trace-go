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
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

// Payloads stores decoded CI Visibility test-cycle payloads received by the mock intake.
type Payloads struct {
	mu       sync.Mutex
	payloads []*Payload
}

// Payload is the decoded CI Visibility test-cycle payload shape used by the fixtures.
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

// StartMockServer starts an agentless CI Visibility intake mock and configures the process environment.
func StartMockServer(settings civisibilitynet.SettingsResponseData) (*httptest.Server, *Payloads, func()) {
	payloads := &Payloads{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/citestcycle":
			payloads.handleTestCycle(w, r)
		case "/api/v2/libraries/tests/services/setting":
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
		case "/api/v2/git/repository/search_commits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
		case "/api/v2/git/repository/packfile", "/api/v2/logs":
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))

	restore := setEnv(map[string]string{
		"DD_CIVISIBILITY_ENABLED":           "true",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED": "true",
		"DD_CIVISIBILITY_AGENTLESS_URL":     server.URL,
		"DD_API_KEY":                        "***",
		"DD_GIT_REPOSITORY_URL":             "https://github.com/DataDog/dd-trace-go.git",
		"DD_GIT_COMMIT_SHA":                 "1234567890abcdef1234567890abcdef12345678",
		"DD_GIT_BRANCH":                     "main",
	})

	return server, payloads, func() {
		restore()
		server.Close()
	}
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

// CheckEventsByType returns events with the given type and panics if the count does not match.
func (e Events) CheckEventsByType(eventType string, count int) Events {
	var result Events
	for _, event := range e {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	if len(result) != count {
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
		panic(fmt.Sprintf("expected exactly %d event(s) with tag %s=%s, got %d", count, tagName, tagValue, len(result)))
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

type envSnapshot struct {
	key   string
	value string
	had   bool
}

func setEnv(values map[string]string) func() {
	snapshots := make([]envSnapshot, 0, len(values))
	for key, value := range values {
		old, had := env.Lookup(key)
		snapshots = append(snapshots, envSnapshot{key: key, value: old, had: had})
		_ = os.Setenv(key, value)
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
