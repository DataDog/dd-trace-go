// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package operation

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// EventListener interface allowing to identify the Go type listened to and
	// dispatch calls to the underlying event listener function.
	EventListener[O Operation, T any] func(O, T)

	// eventRegister implements a thread-safe list of event listeners.
	eventRegister struct {
		listeners eventListenerMap
		mu        sync.RWMutex
	}

	// eventListenerMap is the map of event listeners. The list of listeners are
	// indexed by the operation argument or result type the event listener
	// expects.
	eventListenerMap map[any][]any
)

// addEventListener registers the provided event listener into the provided
// eventRegister.
func addEventListener[O Operation, T any](r *eventRegister, l EventListener[O, T]) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := typeID[EventListener[O, T]]{}
	if r.listeners == nil {
		r.listeners = eventListenerMap{key: {l}}
	} else {
		r.listeners[key] = append(r.listeners[key], l)
	}
}

// emitEvent propagates the provided value to all listeners registered for its
// type up the provided operation's stack. If any of the listeners causes a
// panic, event propagation is aborted by the panic is intercepted and
// swallowed.
func emitEvent[O Operation, T any](r *eventRegister, op O, v T) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("appsec: recovered from an unexpected panic from an event listener: %+v", r)
		}
	}()

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, listener := range r.listeners[typeID[EventListener[O, T]]{}] {
		listener.(EventListener[O, T])(op, v)
	}
}

// Clear removes all regiestered event listeners from the receiving eventRegister.
func (r *eventRegister) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.listeners = nil
}
