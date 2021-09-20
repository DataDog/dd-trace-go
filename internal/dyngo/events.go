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

type (
	EventListenerID interface {
		unregisterFrom(*Operation)
	}
	OnStartEventListenerID  eventListenerID
	OnFinishEventListenerID eventListenerID
	OnDataEventListenerID   eventListenerID
	eventListenerID         uint32
)

var currentEventListenerID eventListenerID

func (id *eventListenerID) atomicIncrement() eventListenerID {
	return eventListenerID(atomic.AddUint32((*uint32)(id), 1))
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
	id := currentEventListenerID.atomicIncrement()
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

func (e *eventManager) OnStart(l OnStartEventListenerFunc) EventListenerID {
	return OnStartEventListenerID(e.onStart.add(makeMapKey(reflect.TypeOf(l).In(1)), l))
}

func (e *eventManager) emitStartEvent(op *Operation, args interface{}) {
	e.emitEvent(op, &e.onStart, args)
}

func (e *eventManager) OnFinish(l OnFinishEventListenerFunc) EventListenerID {
	return OnFinishEventListenerID(e.onFinish.add(makeMapKey(reflect.TypeOf(l).In(1)), l))
}

func (e *eventManager) emitFinishEvent(op *Operation, results interface{}) {
	e.emitEvent(op, &e.onFinish, results)
}

func (e *eventManager) OnData(l OnDataEventListenerFunc) EventListenerID {
	return OnDataEventListenerID(e.onData.add(makeMapKey(reflect.TypeOf(l).In(1)), l))
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
	// OnStartEventListenerFunc func(op Operation, args <T>)
	OnStartEventListenerFunc interface{}
	// OnDataEventListenerFunc func(op Operation, data <T>)
	OnDataEventListenerFunc interface{}
	// OnFinishEventListenerFunc func(op Operation, results <T>)
	OnFinishEventListenerFunc interface{}

	EventListener interface {
		registerTo(*Operation) EventListenerID
	}

	eventListener         struct{ l interface{} }
	onStartEventListener  eventListener
	onDataEventListener   eventListener
	onFinishEventListener eventListener
)

func (o *Operation) Register(l ...EventListener) (ids []EventListenerID) {
	ids = make([]EventListenerID, 0, len(l))
	for _, l := range l {
		ids = append(ids, l.registerTo(o))
	}
	return ids
}

func (o *Operation) Unregister(ids []EventListenerID) {
	for _, id := range ids {
		id.unregisterFrom(o)
	}
}

func (id OnStartEventListenerID) unregisterFrom(op *Operation) {
	forEachOperation(op, func(op *Operation) {
		if op.onStart.remove(eventListenerID(id)) {
			return
		}
	})
}

func (id OnFinishEventListenerID) unregisterFrom(op *Operation) {
	forEachOperation(op, func(op *Operation) {
		if op.onFinish.remove(eventListenerID(id)) {
			return
		}
	})
}

func (id OnDataEventListenerID) unregisterFrom(op *Operation) {
	forEachOperation(op, func(op *Operation) {
		if op.onData.remove(eventListenerID(id)) {
			return
		}
	})
}

func OnStartEventListener(l OnStartEventListenerFunc) EventListener {
	return onStartEventListener{l}
}
func (l onStartEventListener) registerTo(op *Operation) EventListenerID {
	return op.OnStart(l.l)
}

func OnDataEventListener(l OnDataEventListenerFunc) EventListener {
	return onDataEventListener{l}
}
func (l onDataEventListener) registerTo(op *Operation) EventListenerID {
	return op.OnData(l.l)
}

func OnFinishEventListener(l OnFinishEventListenerFunc) EventListener {
	return onFinishEventListener{l}
}
func (l onFinishEventListener) registerTo(op *Operation) EventListenerID {
	return op.OnFinish(l.l)
}
