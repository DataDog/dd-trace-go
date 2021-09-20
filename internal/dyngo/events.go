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
	eventManager struct {
		onStart, onData, onFinish eventRegister

		disabled bool
		mu       sync.RWMutex
	}

	eventRegister struct {
		mu        sync.RWMutex
		listeners eventListenerMap
	}

	// eventListenerMap is the map of event listeners. The list of listeners are indexed by the operation argument or
	// result type the event listener expects. The map key type is a struct of the fully-qualified type name (type
	// package path and name) to uniquely identify the argument/result type instead of using the interface type
	// reflect.Type that can possibly lead to panics if the actual value isn't hashable as required by map keys.
	eventListenerMap map[eventListenerMapKey][]eventListenerMapEntry

	eventListenerMapKey struct {
		pkgPath, name string
	}

	eventListenerMapEntry struct {
		Id       eventListenerID
		Callback interface{}
	}
)

type eventListenerID uint32

var lastID eventListenerID

func nextID() eventListenerID {
	return eventListenerID(atomic.AddUint32((*uint32)(&lastID), 1))
}

func makeMapKey(typ reflect.Type) eventListenerMapKey {
	return eventListenerMapKey{
		pkgPath: typ.PkgPath(),
		name:    typ.Name(),
	}
}

func (r *eventRegister) add(k eventListenerMapKey, l interface{}) eventListenerID {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listeners == nil {
		r.listeners = make(eventListenerMap)
	}
	id := nextID()
	r.listeners[k] = append(r.listeners[k], eventListenerMapEntry{
		Id:       id,
		Callback: l,
	})
	return id
}

func (r *eventRegister) remove(id eventListenerID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, listeners := range r.listeners {
		for i, v := range listeners {
			if v.Id == id {
				r.listeners[k] = append(listeners[:i], listeners[i+1:]...)
				return true
			}
		}
	}
	return false
}

func (r *eventRegister) forEachListener(typ reflect.Type, f func(l interface{})) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.listeners[makeMapKey(typ)] {
		f(e.Callback)
	}
}

func (r *eventRegister) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = nil
}

func (e *eventManager) OnStart(l OnStartEventListenerFunc) UnregisterFunc {
	return registerTo(&e.onStart, l)
}

func (e *eventManager) emitStartEvent(op *Operation, args interface{}) {
	e.emitEvent(op, &e.onStart, args)
}

func (e *eventManager) OnFinish(l OnFinishEventListenerFunc) UnregisterFunc {
	return registerTo(&e.onFinish, l)
}

func (e *eventManager) emitFinishEvent(op *Operation, results interface{}) {
	e.emitEvent(op, &e.onFinish, results)
}

func (e *eventManager) OnData(l OnDataEventListenerFunc) UnregisterFunc {
	return registerTo(&e.onData, l)
}

func (e *eventManager) emitDataEvent(op *Operation, data interface{}) {
	e.emitEvent(op, &e.onData, data)
}

func (e *eventManager) emitEvent(op *Operation, r *eventRegister, args interface{}) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.disabled {
		return
	}
	argv := []reflect.Value{reflect.ValueOf(op), reflect.ValueOf(args)}
	argsT := reflect.TypeOf(args)
	r.forEachListener(argsT, func(l interface{}) {
		reflect.ValueOf(l).Call(argv)
	})
}

type (
	// OnStartEventListenerFunc func(op *Operation, args <T>)
	OnStartEventListenerFunc interface{}
	// OnDataEventListenerFunc func(op *Operation, data <T>)
	OnDataEventListenerFunc interface{}
	// OnFinishEventListenerFunc func(op *Operation, results <T>)
	OnFinishEventListenerFunc interface{}

	EventListener interface {
		registerTo(*Operation) UnregisterFunc
	}

	eventListener         struct{ l interface{} }
	onStartEventListener  eventListener
	onDataEventListener   eventListener
	onFinishEventListener eventListener
)

type UnregisterFunc func()

func (o *Operation) Register(l ...EventListener) UnregisterFunc {
	unregisterFuncs := make([]UnregisterFunc, len(l))
	for i, l := range l {
		unregisterFuncs[i] = l.registerTo(o)
	}
	return func() {
		for _, unregister := range unregisterFuncs {
			unregister()
		}
	}
}

func registerTo(register *eventRegister, l interface{}) UnregisterFunc {
	id := register.add(makeMapKey(reflect.TypeOf(l).In(1)), l)
	return func() {
		register.remove(id)
	}
}

func OnStartEventListener(l OnStartEventListenerFunc) EventListener {
	return onStartEventListener{l}
}
func (l onStartEventListener) registerTo(op *Operation) UnregisterFunc {
	return op.OnStart(l.l)
}

func OnDataEventListener(l OnDataEventListenerFunc) EventListener {
	return onDataEventListener{l}
}
func (l onDataEventListener) registerTo(op *Operation) UnregisterFunc {
	return op.OnData(l.l)
}

func OnFinishEventListener(l OnFinishEventListenerFunc) EventListener {
	return onFinishEventListener{l}
}
func (l onFinishEventListener) registerTo(op *Operation) UnregisterFunc {
	return op.OnFinish(l.l)
}
