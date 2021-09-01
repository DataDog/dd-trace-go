package dyngo

import (
	"reflect"
	"sync"
)

type (
	eventManager struct {
		onStart, onData, onFinish eventRegister

		disabled bool
		mu       sync.RWMutex
	}

	// eventListenerMap is the map of event listeners. The list of listeners are indexed by the operation argument or
	// result type the event listener expects. The map key type is a struct of the fully-qualified type name (type
	// package path and name) to uniquely identify the argument/result type instead of using the interface type
	// reflect.Type that can possibly lead to panics if the actual value isn't hashable as required by map keys.
	eventListenerMap    map[eventListenerMapKey][]interface{}
	eventListenerMapKey struct {
		pkgPath, name string
	}
	eventRegister struct {
		mu        sync.RWMutex
		listeners eventListenerMap
	}
)

func makeMapKey(typ reflect.Type) eventListenerMapKey {
	return eventListenerMapKey{
		pkgPath: typ.PkgPath(),
		name:    typ.Name(),
	}
}

func (r *eventRegister) add(k eventListenerMapKey, l interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listeners == nil {
		r.listeners = make(eventListenerMap)
	}
	r.listeners[k] = append(r.listeners[k], l)
}

func (r *eventRegister) forEachListener(typ reflect.Type, f func(l interface{})) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, l := range r.listeners[makeMapKey(typ)] {
		f(l)
	}
}

func (r *eventRegister) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = nil
}

func (e *eventManager) OnStart(l OnStartEventListenerFunc) {
	e.onStart.add(makeMapKey(reflect.TypeOf(l).In(1)), l)
}

func (e *eventManager) emitStartEvent(op *Operation, args interface{}) {
	e.emitEvent(op, &e.onStart, args)
}

func (e *eventManager) OnFinish(l OnFinishEventListenerFunc) {
	e.onFinish.add(makeMapKey(reflect.TypeOf(l).In(1)), l)
}

func (e *eventManager) emitFinishEvent(op *Operation, results interface{}) {
	e.emitEvent(op, &e.onFinish, results)
}

func (e *eventManager) OnData(l OnDataEventListenerFunc) {
	e.onData.add(makeMapKey(reflect.TypeOf(l).In(1)), l)
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
		registerTo(*Operation)
	}

	eventListener         struct{ l interface{} }
	onStartEventListener  eventListener
	onDataEventListener   eventListener
	onFinishEventListener eventListener
)

func (o *Operation) Register(l ...EventListener) {
	for _, l := range l {
		l.registerTo(o)
	}
}

func OnStartEventListener(l OnStartEventListenerFunc) EventListener { return onStartEventListener{l} }
func (l onStartEventListener) registerTo(op *Operation)             { op.OnStart(l.l) }

func OnDataEventListener(l OnDataEventListenerFunc) EventListener { return onDataEventListener{l} }
func (l onDataEventListener) registerTo(op *Operation)            { op.OnData(l.l) }

func OnFinishEventListener(l OnFinishEventListenerFunc) EventListener { return onFinishEventListener{l} }
func (l onFinishEventListener) registerTo(op *Operation)              { op.OnFinish(l.l) }
