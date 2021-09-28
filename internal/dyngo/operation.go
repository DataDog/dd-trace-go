// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import (
	"fmt"
	"reflect"
	"sync"
)

type (
	// Option is the interface of operation options.
	Option interface {
		apply(s *Operation)
	}

	optionFunc func(s *Operation)
)

func (f optionFunc) apply(s *Operation) {
	f(s)
}

// WithParent allows defining the parent operation of the one being created.
func WithParent(parent *Operation) Option {
	return optionFunc(func(op *Operation) {
		op.parent = parent
	})
}

var root = newOperation()

// Operation structure allowing to subscribe to operation events and to navigate in the operation stack. Events
// bubble-up the operation stack, which allows listening to future events that might happen in the operation lifetime.
type Operation struct {
	parent                    *Operation
	expectedResType           reflect.Type
	onStart, onData, onFinish eventRegister

	disabled bool
	mu       sync.RWMutex
}

// List of registered operations allowing an exhaustive type-checking of operation argument and result types,
// and event listeners.
var (
	// Map of registered operation argument and result types. It stores the operation argument types with their
	// expected result types.
	operationRegister = make(map[reflect.Type]reflect.Type)
	// Map of registered operation result types to find if a given result type was registered. Used to validate
	// finish event listeners.
	operationResRegister = make(map[reflect.Type]struct{})
)

// RegisterOperation registers an operation through its argument and result types that should be used when
// starting and finishing it.
func RegisterOperation(args, res interface{}) {
	argsType, err := validateEventListenerKey(args)
	if err != nil {
		panic(err)
	}
	resType, err := validateEventListenerKey(res)
	if err != nil {
		panic(err)
	}
	if resType, exists := operationRegister[argsType]; exists {
		panic(fmt.Errorf("operation already registered with argument type %s and result type %s", argsType, resType))
	}
	operationRegister[argsType] = resType
	operationResRegister[resType] = struct{}{}
}

// StartOperation starts a new operation along with its arguments and emits a start event with the operation arguments.
func StartOperation(args interface{}, opts ...Option) *Operation {
	expectedResType, err := getOperationRes(reflect.TypeOf(args))
	if err != nil {
		panic(err)
	}
	o := newOperation(opts...)
	o.expectedResType = expectedResType
	if o.parent == nil {
		o.parent = root
	}
	for op := o.Parent(); op != nil; op = op.Parent() {
		op.emitStartEvent(o, args)
	}
	return o
}

func newOperation(opts ...Option) *Operation {
	op := &Operation{}
	for _, opt := range opts {
		opt.apply(op)
	}
	return op
}

func (o *Operation) emitStartEvent(eventOp *Operation, args interface{}) {
	o.emitEvent(&o.onStart, eventOp, args)
}

// Parent return the parent operation. It returns nil for the root operation.
func (o *Operation) Parent() *Operation {
	return o.parent
}

// Finish finishes the operation along with its results and emits a finish event with the operation results.
// The operation is then disabled and its event listeners removed.
func (o *Operation) Finish(results interface{}) {
	defer o.disable()
	if err := validateExpectedRes(reflect.TypeOf(results), o.expectedResType); err != nil {
		panic(err)
	}
	for op := o; op != nil; op = op.Parent() {
		op.emitFinishEvent(o, results)
	}
}

func (o *Operation) emitFinishEvent(eventOp *Operation, results interface{}) {
	o.emitEvent(&o.onFinish, eventOp, results)
}

func (o *Operation) disable() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.disabled = true
	o.onStart.clear()
	o.onData.clear()
	o.onFinish.clear()
}

// EmitData allows emitting operation data usually computed in the operation lifetime. Examples include parsed values
// like an HTTP request's JSON body, the HTTP raw body, etc. - data that is obtained by monitoring the operation
// execution, possibly throughout several executions.
func (o *Operation) EmitData(data interface{}) {
	for op := o; op != nil; op = op.Parent() {
		op.emitDataEvent(o, data)
	}
}

func (o *Operation) emitDataEvent(eventOp *Operation, data interface{}) {
	o.emitEvent(&o.onData, eventOp, data)
}

// emitEvent calls the event listeners of the given event register when it is not disabled.
func (o *Operation) emitEvent(r *eventRegister, op *Operation, v interface{}) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}
	r.callListeners(op, v)
}

// UnregisterFunc is a function allowing to unregister from an operation the previously registered event listeners.
type UnregisterFunc func()

// Register allows to register the given event listeners to the operation. An unregistration function is returned
// allowing to unregister the event listeners from the operation.
func (o *Operation) Register(l ...EventListener) UnregisterFunc {
	ids := make([]eventUnregister, len(l))
	for i, l := range l {
		ids[i] = l.registerTo(o)
	}
	return func() {
		for _, id := range ids {
			id.unregisterFrom(o)
		}
	}
}

func (o *Operation) OnStart(argsPtr interface{}, l OnStartEventListenerFunc) {
	argsType, err := validateEventListenerKey(argsPtr)
	if err != nil {
		panic(err)
	}
	if err := validateStartOperationArgs(argsType); err != nil {
		panic(err)
	}
	o.onStart.add(argsType, l)
}

func (o *Operation) OnData(dataPtr interface{}, l OnDataEventListenerFunc) {
	dataType, err := validateEventListenerKey(dataPtr)
	if err != nil {
		panic(err)
	}
	o.onData.add(dataType, l)
}

func (o *Operation) OnFinish(resPtr interface{}, l OnFinishEventListenerFunc) {
	resType, err := validateEventListenerKey(resPtr)
	if err != nil {
		panic(err)
	}
	if err := validateFinishOperationRes(resType); err != nil {
		panic(err)
	}
	o.onFinish.add(resType, l)
}

func validateExpectedRes(res, expectedRes reflect.Type) error {
	if res != expectedRes {
		return fmt.Errorf("unexpected operation result: expecting type %s instead of %s", expectedRes, res)
	}
	return nil
}

func getOperationRes(args reflect.Type) (res reflect.Type, err error) {
	res, ok := operationRegister[args]
	if !ok {
		return nil, fmt.Errorf("unexpected operation: unknow operation argument type %s", args)
	}
	return res, nil
}

func validateStartOperationArgs(argsType reflect.Type) error {
	if _, ok := operationRegister[argsType]; !ok {
		return fmt.Errorf("unexpected use of an unregistered operation of argument type %s", argsType)
	}
	return nil
}

func validateFinishOperationRes(resType reflect.Type) error {
	if _, ok := operationResRegister[resType]; !ok {
		return fmt.Errorf("unexpected use of an unregistered operation of result type %s", resType)
	}
	return nil
}
