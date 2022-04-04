// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package instrumentation holds code commonly used between all instrumentation declinations (currently httpsec/grpcsec).
package instrumentation

import (
	"encoding/json"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// TagsHolder wraps a map holding tags. The purpose of this struct is to be used by composition in an Operation
// to allow said operation to handle tags addition/retrieval. See httpsec/http.go and grpcsec/grpc.go.
type TagsHolder struct {
	tags map[string]interface{}
}

// NewTagsHolder returns a new instance of a TagsHolder struct.
func NewTagsHolder() TagsHolder {
	return TagsHolder{tags: map[string]interface{}{}}
}

// AddTag adds the key/value pair to the tags map
func (m *TagsHolder) AddTag(k string, v interface{}) {
	m.tags[k] = v
}

// Tags returns the tags map
func (m *TagsHolder) Tags() map[string]interface{} {
	return m.tags
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
