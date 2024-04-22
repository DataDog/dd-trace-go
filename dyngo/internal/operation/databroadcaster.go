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
	// DataListener is a function that gets called with the specified data type.
	DataListener[T any] func(T)

	// dataBroadcaster is a thread-safe list of data listeners.
	dataBroadcaster struct {
		listeners dataListenerMap
		mu        sync.RWMutex
	}
	// dataListenerMap is the map of data listeners.
	dataListenerMap map[any][]any
)

// addDataListener registers a new listener to the provided dataBroadcaster.
func addDataListener[T any](b *dataBroadcaster, l DataListener[T]) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := typeID[DataListener[T]]{}
	if b.listeners == nil {
		b.listeners = dataListenerMap{key: {l}}
	} else {
		b.listeners[key] = append(b.listeners[key], l)
	}
}

// emitData propagates a data event to all registered listeners. If any listener
// causes a panic, event propagation is aborted, but the panic is recovered and
// logged.
func emitData[T any](b *dataBroadcaster, v T) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("appsec: recovered from an unexpected panic from an event listener: %+v", r)
		}
	}()

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, listener := range b.listeners[typeID[DataListener[T]]{}] {
		listener.(DataListener[T])(v)
	}
}

// clear removes all listeners from the receiving dataBroadcaster.
func (b *dataBroadcaster) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.listeners = nil
}
