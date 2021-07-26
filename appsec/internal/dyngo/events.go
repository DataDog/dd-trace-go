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

	eventListenerMap map[reflect.Type][]interface{}
	eventRegister    struct {
		mu        sync.RWMutex
		listeners eventListenerMap
	}
)

func (r *eventRegister) add(typ reflect.Type, l interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listeners == nil {
		r.listeners = make(eventListenerMap)
	}
	r.listeners[typ] = append(r.listeners[typ], l)
}

func (r *eventRegister) forEachListener(typ reflect.Type, f func(l interface{})) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, l := range r.listeners[typ] {
		f(l)
	}
}

func (r *eventRegister) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = nil
}

func (e *eventManager) OnStart(l OnStartEventListenerFunc) {
	e.onStart.add(reflect.TypeOf(l).In(1), l)
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

func (e *eventManager) emitStartEvent(op *Operation, args interface{}) {
	e.emitEvent(op, &e.onStart, args)
}

func (e *eventManager) OnFinish(l OnFinishEventListenerFunc) {
	e.onFinish.add(reflect.TypeOf(l).In(1), l)
}

func (e *eventManager) emitFinishEvent(op *Operation, results interface{}) {
	e.emitEvent(op, &e.onFinish, results)
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

	onStartEventListener struct {
		l interface{}
	}
	onFinishEventListener struct {
		l interface{}
	}
)

func (o *Operation) Register(l ...EventListener) {
	for _, l := range l {
		l.registerTo(o)
	}
}

func OnStart(l OnStartEventListenerFunc) EventListener  { return onStartEventListener{l} }
func (l onStartEventListener) registerTo(op *Operation) { op.OnStart(l.l) }

func OnFinish(l OnFinishEventListenerFunc) EventListener { return onFinishEventListener{l} }
func (l onFinishEventListener) registerTo(op *Operation) { op.OnFinish(l.l) }
