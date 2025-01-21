// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package civisibility

import (
	"fmt"
)

type (
	mockPayloads []*mockPayload
	mockPayload  struct {
		Version  int          `json:"version"`
		Metadata mockMetadata `json:"metadata"`
		Events   mockEvents   `json:"events"`
	}
	mockMetadata struct {
		All            mockMetadataAll   `json:"*"`
		TestSessionEnd mockMetadataOther `json:"test_session_end"`
		TestModuleEnd  mockMetadataOther `json:"test_module_end"`
		TestSuiteEnd   mockMetadataOther `json:"test_suite_end"`
		Test           mockMetadataOther `json:"test"`
	}
	mockMetadataAll struct {
		Language       string `json:"language"`
		LibraryVersion string `json:"library_version"`
		RuntimeID      string `json:"runtime-id"`
	}
	mockMetadataOther struct {
		TestSessionName string `json:"test_session.name"`
	}
	mockEvents []mockEvent
	mockEvent  struct {
		Type    string      `json:"type"`
		Version int         `json:"version"`
		Content mockContent `json:"content"`
	}
	mockContent struct {
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
)

func (m *mockPayloads) GetEvents() mockEvents {
	var events mockEvents
	for _, payload := range *m {
		events = append(events, payload.Events...)
	}
	return events
}

func (m mockEvents) ShowResourceNames() mockEvents {
	for i, event := range m {
		_, _ = fmt.Printf("  [%d] = %v\n", i, event.Content.Resource)
	}
	return m
}

func (m mockEvents) GetEventsByType(eventType string) mockEvents {
	var events mockEvents
	for _, event := range m {
		if event.Type == eventType {
			events = append(events, event)
		}
	}
	return events
}

func (m mockEvents) CheckEventsByType(eventType string, count int) mockEvents {
	events := m.GetEventsByType(eventType)
	numOfEvents := len(events)
	if numOfEvents != count {
		panic(fmt.Sprintf("expected exactly %d event(s) with type name: %s, got %d", count, eventType, numOfEvents))
	}

	return events
}

func (m mockEvents) GetEventsByResourceName(resourceName string) mockEvents {
	var events mockEvents
	for _, event := range m {
		if event.Content.Resource == resourceName {
			events = append(events, event)
		}
	}
	return events
}

func (m mockEvents) CheckEventsByResourceName(resourceName string, count int) mockEvents {
	events := m.GetEventsByResourceName(resourceName)
	numOfEvents := len(events)
	if numOfEvents != count {
		panic(fmt.Sprintf("expected exactly %d event(s) with resource name: %s, got %d", count, resourceName, numOfEvents))
	}

	return events
}

func (m mockEvents) GetEventsByTagName(tagName string) mockEvents {
	var events mockEvents
	for _, event := range m {
		if _, ok := event.Content.Meta[tagName]; ok {
			events = append(events, event)
		}
	}
	return events
}

func (m mockEvents) CheckEventsByTagName(tagName string, count int) mockEvents {
	events := m.GetEventsByTagName(tagName)
	numOfEvents := len(events)
	if numOfEvents != count {
		panic(fmt.Sprintf("expected exactly %d event(s) with tag name: %s, got %d", count, tagName, numOfEvents))
	}

	return events
}

func (m mockEvents) GetEventsByTagAndValue(tagName string, tagValue string) mockEvents {
	var events mockEvents
	for _, event := range m {
		if value, ok := event.Content.Meta[tagName]; ok && value == tagValue {
			events = append(events, event)
		}
	}
	return events
}

func (m mockEvents) CheckEventsByTagAndValue(tagName string, tagValue string, count int) mockEvents {
	events := m.GetEventsByTagAndValue(tagName, tagValue)
	numOfEvents := len(events)
	if numOfEvents != count {
		panic(fmt.Sprintf("expected exactly %d event(s) with tag name: %s, got %d", count, tagName, numOfEvents))
	}

	return events
}

func (m mockEvents) Except(events ...mockEvents) mockEvents {
	var filtered mockEvents
	for _, event := range m {
		contains := false
		for _, eventArray := range events {
			for _, ev := range eventArray {
				if event.Content.Resource == ev.Content.Resource {
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

func (m mockEvents) HasCount(count int) mockEvents {
	numOfEvents := len(m)
	if numOfEvents != count {
		m.ShowResourceNames()
		panic(fmt.Sprintf("expected exactly %d event(s), got %d", count, numOfEvents))
	}

	return m
}
