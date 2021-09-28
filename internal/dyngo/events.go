// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import (
	"reflect"
	"sync"
	"sync/atomic"
)

type (
	// eventRegister implements a thread-safe list of event listeners.
	eventRegister struct {
		mu        sync.RWMutex
		listeners eventListenerMap
	}

	// eventListenerMap is the map of event listeners. The list of listeners are indexed by the operation argument or
	// result type the event listener expects. The map key type is a struct of the fully-qualified type name (type
	// package path and name) to uniquely identify the argument/result type instead of using the interface type
	// reflect.Type that can possibly lead to panics if the actual value isn't hashable as required by map keys.
	eventListenerMap      map[reflect.Type][]eventListenerMapEntry
	eventListenerMapEntry struct {
		ID       eventListenerID
		Callback EventListenerFunc
	}
	// eventListenerID is the unique ID of an event when registering it. It allows to find it back and remove it from
	// the list of event listeners when unregistering it.
	eventListenerID uint32
	// onStartEventListenerID is a helper type implementing eventUnregister to unregister the ID from the correct
	// event register, i.e. the start event register.
	onStartEventListenerID eventListenerID
	// onFinishEventListenerID is a helper type implementing eventUnregister to unregister the ID from the correct
	//	// event register, i.e. the finish event register.
	onFinishEventListenerID eventListenerID
	// onDataEventListenerID is a helper type implementing eventUnregister to unregister the ID from the correct
	//	// event register, i.e. the data event register.
	onDataEventListenerID eventListenerID
	// eventUnregister interface to dynamically dispatch the unregistration of the ID to the correct event register
	// store of the operation.
	eventUnregister interface {
		unregisterFrom(*Operation)
	}
)

// lastID is the last event listener ID that was given to the latest event listener.
var lastID eventListenerID

// nextID atomically increments lastID and returns the new event listener ID to use.
func nextID() eventListenerID {
	return eventListenerID(atomic.AddUint32((*uint32)(&lastID), 1))
}

func (r *eventRegister) add(k reflect.Type, l EventListenerFunc) eventListenerID {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listeners == nil {
		r.listeners = make(eventListenerMap)
	}
	id := nextID()
	r.listeners[k] = append(r.listeners[k], eventListenerMapEntry{
		ID:       id,
		Callback: l,
	})
	return id
}

func (r *eventRegister) remove(id eventListenerID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// TODO(julio): optimize by providing the key type too
	for k, listeners := range r.listeners {
		for i, v := range listeners {
			if v.ID == id {
				r.listeners[k] = append(listeners[:i], listeners[i+1:]...)
				return
			}
		}
	}
	return
}

func (r *eventRegister) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = nil
}

func (r *eventRegister) callListeners(op *Operation, data interface{}) {
	dataT := reflect.TypeOf(data)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.listeners[dataT] {
		e.Callback(op, data)
	}
}

type (
	// OnStartEventListenerFunc is a start event listener function.
	OnStartEventListenerFunc EventListenerFunc
	// OnDataEventListenerFunc is a data event listener function.
	OnDataEventListenerFunc EventListenerFunc
	// OnFinishEventListenerFunc is a finish event listener function.
	OnFinishEventListenerFunc EventListenerFunc

	// EventListenerFunc is an alias to the event listener function.
	// The listener must perform the type-assertion to the expected type.
	// Helper function closures should be used to enforce the type-safety,
	// such as:
	//
	//   func (l func(*dyngo.Operation, T)) EventListenerFunc {
	//      return func (op *dyngo.Operation, v interface{}) {
	//         l(op, v.(T))
	//      }
	//   }
	//
	EventListenerFunc = func(op *Operation, v interface{})

	// EventListener interface implemented by event listeners. They can be passed used in the instrumentation
	// descriptors and the operation Register method.
	EventListener interface {
		// registerTo dynamically dispatches the event listener registration to the proper event register.
		registerTo(*Operation) eventUnregister
	}

	// eventListener is a structure describing an event listener.
	eventListener struct {
		// key is the listened event type, used as key type in the event register.
		key reflect.Type
		l   EventListenerFunc
	}
	// onStartEventListener is a helper type implementing the EventListener interface.
	onStartEventListener eventListener
	// onDataEventListener is a helper type implementing the EventListener interface.
	onDataEventListener eventListener
	// onFinishEventListener is a helper type implementing the EventListener interface.
	onFinishEventListener eventListener
)

// OnStartEventListener returns a start event listener whose argument type is described by the argsPtr argument which
// must be a nil pointer to the expected argument type. For example:
//
//     // Create a finish event listener whose result type is MyOpArgs
//     OnStartEventListener((*MyOpArgs), func(op *Operation, v interface{}) {
//         results := v.(MyOpArgs)
//     })
//
func OnStartEventListener(argsPtr interface{}, l OnStartEventListenerFunc) EventListener {
	argsType, err := validateEventListenerKey(argsPtr)
	if err != nil {
		panic(err)
	}
	return onStartEventListener{key: argsType, l: l}
}

func (l onStartEventListener) registerTo(op *Operation) eventUnregister {
	return onStartEventListenerID(op.onStart.add(l.key, l.l))
}

func (id onStartEventListenerID) unregisterFrom(op *Operation) {
	op.onStart.remove(eventListenerID(id))
}

// OnDataEventListener returns a data event listener whose type is described by the dataPtr argument which must be a nil
// pointer to the expected data type. For example:
//
//     // Create a data event listener whose result type is MyOpData
//     OnDataEventListener((*MyOpData), func(op *Operation, v interface{}) {
//         results := v.(MyOpData)
//     })
//
func OnDataEventListener(dataPtr interface{}, l OnDataEventListenerFunc) EventListener {
	dataType, err := validateEventListenerKey(dataPtr)
	if err != nil {
		panic(err)
	}
	return onDataEventListener{key: dataType, l: l}
}

func (l onDataEventListener) registerTo(op *Operation) eventUnregister {
	return onDataEventListenerID(op.onData.add(l.key, l.l))
}

func (id onDataEventListenerID) unregisterFrom(op *Operation) {
	op.onData.remove(eventListenerID(id))
}

// OnFinishEventListener returns a finish event listener whose result type is described by the resPtr argument which
// must be a nil pointer to the expected result type. For example:
//
//     // Create a finish event listener whose result type is MyOpResults
//     OnFinishEventListener((*MyOpResults), func(op *Operation, v interface{}) {
//         results := v.(MyOpResults)
//     })
//
func OnFinishEventListener(resPtr interface{}, l OnFinishEventListenerFunc) EventListener {
	resType, err := validateEventListenerKey(resPtr)
	if err != nil {
		panic(err)
	}
	return onFinishEventListener{key: resType, l: l}
}

func (l onFinishEventListener) registerTo(op *Operation) eventUnregister {
	return onFinishEventListenerID(op.onFinish.add(l.key, l.l))
}

func (id onFinishEventListenerID) unregisterFrom(op *Operation) {
	op.onFinish.remove(eventListenerID(id))
}
