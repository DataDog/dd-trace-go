// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package instrumentation

import (
	"encoding/json"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// This file holds code commonly used between all instrumentation declinations (currently httpsec/grpcsec)

// MetricsHolder wraps a map holding metrics. The purpose of this struct is to be used by composition in an Operation
// to allow said operation to handle metrics addition/retrieval. See httpsec/http.go and grpcsec/grpc.go.
type MetricsHolder struct {
	metrics map[string]interface{}
}

// AddMetric adds the key/value pair to the metrics map
func (m *MetricsHolder) AddMetric(k string, v interface{}) {
	m.metrics[k] = v
}

// Metrics returns the metrics map
func (m *MetricsHolder) Metrics() map[string]interface{} {
	return m.metrics
}

// NewMetricsHolder returns a new instance of a MetricsHolder struct.
func NewMetricsHolder() MetricsHolder {
	return MetricsHolder{metrics: map[string]interface{}{}}
}

// SecurityEventsHolder is a wrapper around a thread safe security events slice. The purpose of this struct is to be
// used by composition in an Operation to allow said operation to handle security events addition/retrieval.
// See httpsec/http.go and grpcsec/grpc.go.
type SecurityEventsHolder struct {
	events []json.RawMessage
	mu     sync.Mutex
}

// AddSecurityEvents adds the security events to the collected events list.
// Thread safe.
func (s *SecurityEventsHolder) AddSecurityEvents(events ...json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
}

// Events returns the list of stored events.
func (s *SecurityEventsHolder) Events() []json.RawMessage {
	return s.events
}

// SetTags fills the span tags using the key/value pairs found in `tags`
func SetTags(span ddtrace.Span, tags map[string]interface{}) {
	for k, v := range tags {
		span.SetTag(k, v)
	}
}
