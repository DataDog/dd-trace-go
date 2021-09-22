// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import (
	"fmt"
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
	eventListenerMap map[reflect.Type][]eventListenerMapEntry

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

func (r *eventRegister) add(k reflect.Type, l interface{}) eventListenerID {
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

func (r *eventRegister) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = nil
}

func (e *eventManager) OnStart(l OnStartEventListenerFunc) UnregisterFunc {
	argType, err := validateStartEventListener(l)
	if err != nil {
		panic(err)
	}
	return registerTo(&e.onStart, argType, l)
}

func (e *eventManager) emitStartEvent(op *Operation, args interface{}) {
	e.emitEvent(op, &e.onStart, args)
}

func (e *eventManager) OnFinish(l OnFinishEventListenerFunc) UnregisterFunc {
	resType, err := validateFinishEventListener(l)
	if err != nil {
		panic(err)
	}
	return registerTo(&e.onFinish, resType, l)
}

func (e *eventManager) emitFinishEvent(op *Operation, results interface{}) {
	e.emitEvent(op, &e.onFinish, results)
}

func (e *eventManager) OnData(l OnDataEventListenerFunc) UnregisterFunc {
	dataType, err := validateEventListenerFunc(l)
	if err != nil {
		panic(err)
	}
	return registerTo(&e.onData, dataType, l)
}

func (e *eventManager) emitDataEvent(op *Operation, data interface{}) {
	e.emitEvent(op, &e.onData, data)
}

func (e *eventManager) emitEvent(op *Operation, r *eventRegister, data interface{}) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.disabled {
		return
	}
	r.callListeners(op, data)
}

func (r *eventRegister) callListeners(op *Operation, data interface{}) {
	dataV := reflect.ValueOf(data)
	args := []reflect.Value{reflect.ValueOf(op), dataV}
	dataT := dataV.Type()

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.listeners[dataT] {
		v := reflect.ValueOf(e.Callback)
		if v.Type().NumIn() == 1 {
			v.Call(args[1:])
		} else {
			v.Call(args)
		}
	}
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

func registerTo(register *eventRegister, keyType reflect.Type, l interface{}) UnregisterFunc {
	id := register.add(keyType, l)
	return func() {
		register.remove(id)
	}
}

func validateStartEventListener(l OnStartEventListenerFunc) (reflect.Type, error) {
	argType, err := validateEventListenerFunc(l)
	if err != nil {
		return nil, err
	}
	if _, exists := operationRegister[argType]; !exists {
		return nil, fmt.Errorf("cannot listen to a non registered operation argument %s", argType)
	}
	return argType, nil
}

func validateFinishEventListener(l OnFinishEventListenerFunc) (reflect.Type, error) {
	resType, err := validateEventListenerFunc(l)
	if err != nil {
		return nil, err
	}
	if _, exists := operationResRegister[resType]; !exists {
		return nil, fmt.Errorf("cannot listen to a non registered operation result %s", resType)
	}
	return resType, nil
}

// Validate the event listener is a function of the form `func (*Operation, T)` or `func(T)`.
// It returns T when the function is valid. An error otherwise.
func validateEventListenerFunc(l interface{}) (keyType reflect.Type, err error) {
	listenerType := reflect.TypeOf(l)
	if kind := listenerType.Kind(); kind != reflect.Func {
		return nil, fmt.Errorf("unexpected event listener type %s instead of a function", kind.String())
	}

	operationType := reflect.TypeOf((*Operation)(nil))
	switch listenerType.NumIn() {
	case 1:
		// Expecting func (T)
		argType := listenerType.In(0)
		if argType == operationType {
			return nil, fmt.Errorf("unexpected type of event listener argument: the first argument should not be %s", operationType)
		}
		return argType, nil

	case 2:
		// Expecting func (*Operation, T)
		if listenerType.In(0) != operationType {
			return nil, fmt.Errorf("unexpected type of event listener argument: the first argument should be %s", operationType)
		}
		return listenerType.In(1), nil

	default:
		return nil, fmt.Errorf("unexpected number of event listener arguments")
	}
}
