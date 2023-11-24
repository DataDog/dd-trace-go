// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package trace

import (
	"sync"
)

// SecurityEventsHolder is a wrapper around a thread safe security events slice.
// The purpose of this struct is to be used by composition in an Operation to
// allow said operation to handle security events addition/retrieval.
type SecurityEventsHolder struct {
	events []any
	mu     sync.RWMutex
}

// AddSecurityEvents adds the security events to the collected events list.
// Thread safe.
func (s *SecurityEventsHolder) AddSecurityEvents(events []any) {
	if len(events) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
}

// Events returns the list of stored events.
func (s *SecurityEventsHolder) Events() []any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy, since the lock is released upon return.
	clone := make([]any, len(s.events))
	for i, e := range s.events {
		clone[i] = e
	}
	return clone
}

// ClearEvents clears the list of stored events
func (s *SecurityEventsHolder) ClearEvents() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[0:0]
}
