// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package operation

import (
	"sync"
)

// Operation interface type allowing to register event listeners to the
// operation. The event listeners will be automatically removed from the
// operation once it finishes so that it no longer can be called on finished
// operations.
type Operation interface {
	// Parent returns the parent operation, or nil for the root operation.
	Parent() Operation

	// unwrap is an internal method guaranteeing only *operation implements Operation.
	unwrap() *operation
}

// ArgOf marks a particular type as being the argument type of a given operation
// type. This allows this type to be listened to by an operation start listener.
// This removes the possibility of incorrectly pairing an operation and payload
// when setting up listeners, as it allows compiler-assisted coherence checks.
type ArgOf[O Operation] interface {
	IsArgOf(O)
}

// ResultOf marks a particular type as being the result type of a given
// operation. This allows this type to be listened to by an operation finish
// listener.
// This removes the possibility of incorrectly pairing an operation and payload
// when setting up listeners, as it allows compiler-assisted coherence checks.
type ResultOf[O Operation] interface {
	IsResultOf(O)
}

// operation structure allowing to subscribe to operation events and to
// navigate in the operation stack. Events
// bubble-up the operation stack, which allows listening to future events that
// might happen in the operation lifetime.
type operation struct {
	parent *operation
	eventRegister
	dataBroadcaster

	disabled bool
	mu       sync.RWMutex
}

// New creates and returns a new operation. If no parent is provided, the
// current root operation is used.
func New(parent Operation) Operation {
	if parent == nil {
		parent = CurrentRoot()
	}
	var parentOp *operation
	if parent != nil {
		parentOp = parent.unwrap()
	}
	return &operation{parent: parentOp}
}

func (o *operation) Parent() Operation {
	return o.parent
}

// This is the one true Operation implementation!
func (o *operation) unwrap() *operation { return o }

// On registers and event listener that will be called when the operation
// is started.
func On[O Operation, E ArgOf[O]](op Operation, l EventListener[O, E]) {
	o := op.unwrap()

	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}

	addEventListener(&o.eventRegister, l)
}

// OnFinish registers an event listener that will be called when the operation
// finishes.
func OnFinish[O Operation, E ResultOf[O]](op Operation, l EventListener[O, E]) {
	o := op.unwrap()

	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}

	addEventListener(&o.eventRegister, l)
}

// OnData registers a data event listener that will be called whenever the
// operation produces the listened to data type.
func OnData[T any](op Operation, l DataListener[T]) {
	o := op.unwrap()

	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}

	addDataListener(&o.dataBroadcaster, l)
}

func Start[O Operation, E ArgOf[O]](op O, args E) {
	for current := op.unwrap().parent; current != nil; current = current.parent {
		emitEvent(&current.eventRegister, op, args)
	}
}

// EmitData sends a data event up the operation stack. Listeners will be matched
// based on `T`. Callers may need to manually specify T when the static type of
// the value is more specific that the intended data event type.
func EmitData[T any](op Operation, data T) {
	o := op.unwrap()

	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}

	// Bubble up the data to the stack of operations. Contrary to events,
	// we also send the data to ourselves since SDK operations are leaf operations
	// that both emit and listen for data (errors).
	for current := o; current != nil; current = current.parent {
		emitData(&current.dataBroadcaster, data)
	}
}

func Finish[O Operation, E ResultOf[O]](op O, results E) {
	o := op.unwrap()
	defer o.disable()

	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.disabled {
		return
	}

	for current := o; current != nil; current = current.parent {
		emitEvent(&current.eventRegister, op, results)
	}
}

// disable marks the operation as disabled and remove all its event listeners.
func (o *operation) disable() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.disabled {
		return
	}

	o.disabled = true
	o.eventRegister.clear()
}
