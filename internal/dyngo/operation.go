// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import (
	"fmt"
	"reflect"
)

type (
	Option interface {
		apply(s *Operation)
	}

	optionFunc func(s *Operation)
)

func (f optionFunc) apply(s *Operation) {
	f(s)
}

func WithParent(parent *Operation) Option {
	return optionFunc(func(op *Operation) {
		op.parent = parent
	})
}

var root = newOperation()

type Operation struct {
	parent          *Operation
	expectedResType reflect.Type
	eventManager
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
	forEachOperation(o.Parent(), func(op *Operation) {
		op.emitStartEvent(o, args)
	})
	return o
}

func newOperation(opts ...Option) *Operation {
	op := &Operation{}
	for _, opt := range opts {
		opt.apply(op)
	}
	return op
}

func (o *Operation) Parent() *Operation {
	return o.parent
}

func (o *Operation) Finish(results interface{}) {
	defer o.disable()
	if err := validateExpectedRes(reflect.TypeOf(results), o.expectedResType); err != nil {
		panic(err)
	}
	forEachOperation(o, func(op *Operation) {
		op.emitFinishEvent(o, results)
	})
}

func (e *eventManager) disable() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disabled = true
	e.onStart.clear()
	e.onData.clear()
	e.onFinish.clear()
}

func (o *Operation) EmitData(data interface{}) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}
	forEachOperation(o, func(op *Operation) {
		op.emitDataEvent(o, data)
	})
}

type UnregisterFunc func()

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

func forEachOperation(op *Operation, do func(op *Operation)) {
	for ; op != nil; op = op.Parent() {
		do(op)
	}
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
