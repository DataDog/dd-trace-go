// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenttest

import "fmt"

// Span holds the data of a single collected span. Meta and Metrics contain the
// raw string and numeric tags respectively; Tags is a merged view of both plus
// top-level attributes (name, service, resource, type) for convenience.
type Span struct {
	SpanID    uint64
	TraceID   uint64
	ParentID  uint64
	Service   string
	Operation string
	Resource  string
	Type      string
	Start     int64 // unix nanoseconds
	Duration  int64 // nanoseconds
	Error     int32
	Meta      map[string]string
	Metrics   map[string]float64
	Tags      map[string]any // merged view: meta + metrics + top-level attrs
	Children  []*Span
}

// spanCondition pairs a predicate with a human-readable description for diagnostics.
type spanCondition struct {
	fn   func(*Span) bool
	desc string
}

// SpanMatch is a builder for span matching conditions. Create one with [With]
// and chain methods to add conditions. Pass the result to [Agent.FindSpan] or
// [Agent.RequireSpan].
type SpanMatch struct {
	conditions []spanCondition
}

// With returns a new empty SpanMatch builder. Chain methods like Service,
// Operation, Tag, etc. to add matching conditions.
func With() *SpanMatch {
	return &SpanMatch{}
}

// Service adds a condition that the span's service must equal the given value.
func (m *SpanMatch) Service(service string) *SpanMatch {
	m.conditions = append(m.conditions, spanCondition{
		fn:   func(s *Span) bool { return s.Service == service },
		desc: fmt.Sprintf("Service == %q", service),
	})
	return m
}

// Operation adds a condition that the span's operation name must equal the given value.
func (m *SpanMatch) Operation(operation string) *SpanMatch {
	m.conditions = append(m.conditions, spanCondition{
		fn:   func(s *Span) bool { return s.Operation == operation },
		desc: fmt.Sprintf("Operation == %q", operation),
	})
	return m
}

// Resource adds a condition that the span's resource must equal the given value.
func (m *SpanMatch) Resource(resource string) *SpanMatch {
	m.conditions = append(m.conditions, spanCondition{
		fn:   func(s *Span) bool { return s.Resource == resource },
		desc: fmt.Sprintf("Resource == %q", resource),
	})
	return m
}

// Type adds a condition that the span's type must equal the given value.
func (m *SpanMatch) Type(spanType string) *SpanMatch {
	m.conditions = append(m.conditions, spanCondition{
		fn:   func(s *Span) bool { return s.Type == spanType },
		desc: fmt.Sprintf("Type == %q", spanType),
	})
	return m
}

// Tag adds a condition that the span's merged Tags map must contain the given
// key with the given value.
func (m *SpanMatch) Tag(key string, value any) *SpanMatch {
	m.conditions = append(m.conditions, spanCondition{
		fn: func(s *Span) bool {
			v, ok := s.Tags[key]
			return ok && v == value
		},
		desc: fmt.Sprintf("Tags[%q] == %v", key, value),
	})
	return m
}

// ParentOf adds a condition that the span's parent ID must equal the given value.
func (m *SpanMatch) ParentOf(parentID uint64) *SpanMatch {
	m.conditions = append(m.conditions, spanCondition{
		fn:   func(s *Span) bool { return s.ParentID == parentID },
		desc: fmt.Sprintf("ParentID == %d", parentID),
	})
	return m
}

// Matches reports whether the span satisfies all conditions in this SpanMatch.
func (m *SpanMatch) Matches(s *Span) bool {
	for _, c := range m.conditions {
		if !c.fn(s) {
			return false
		}
	}
	return true
}

// FailedConditions returns human-readable descriptions of conditions that did
// not match the given span, for use in diagnostic output.
func (m *SpanMatch) FailedConditions(s *Span) []string {
	var failed []string
	for _, c := range m.conditions {
		if !c.fn(s) {
			failed = append(failed, c.desc)
		}
	}
	return failed
}
