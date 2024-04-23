// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package dyngo is the Go implementation of Datadog's Instrumentation Gateway
// which provides an event-based instrumentation API based on a stack
// representation of instrumented functions along with nested event listeners.
// It allows to both correlate past and future function calls in order to
// react and monitor specific function call scenarios, while keeping the
// monitoring state local to the monitoring logic thanks to nested Go function
// closures.
//
// Developers are not expected to use dyngo directly, but rather though the
// higher level APIs provided in the gopkg.in/DataDog/dd-trace-go.v1/contrib
// packages, or by using github.com/datadog/orchestrion for compile-time
// automatic instrumentation.
package dyngo

import (
	"github.com/datadog/dd-trace-go/dyngo/internal/domainstate"
	"github.com/datadog/dd-trace-go/dyngo/internal/operation"
)

type (
	// Operation interface type allowing to register event listeners to the
	// operation. The event listeners will be automatically removed from the
	// operation once it finishes so that it no longer can be called on finished
	// operations.
	Operation = operation.Operation
)

// ClearRootOperation resets the current root operation to nil. This effectively
// stops all listening by registered products for new operations.
func ClearRootOperation() {
	operation.SwapRoot(nil)
}

// SwapToNewRootOperation creates a new root operation, starts all products
// registered with activated domains, and replaces the current root operation
// with the new one.
func SwapToNewRootOperation() {
	newRoot := operation.NewRoot()
	for _, dom := range domainstate.AllDomains() {
		domainstate.StartProducts(dom, newRoot)
	}
	operation.SwapRoot(newRoot)
}

// OnData registers a data listener that will be called when the operation emits
// the specified data type.
//
// OnData listeners should be registered before the operation is started,
// otherwise any data event emitted by operation start listeners may be missed.
func OnData[T any](op Operation, l operation.DataListener[T]) {
	operation.OnData(op, l)
}

// On registers and event listener that will be called when the operation
// is started.
func On[O Operation, E operation.ArgOf[O]](op Operation, l operation.EventListener[O, E]) {
	operation.On(op, l)
}

// EmitData sends a data event up the operation stack. Listeners will be matched
// based on `T`. Callers may need to manually specify T when the static type of
// the value is more specific that the intended data event type.
func EmitData[T any](op Operation, data T) {
	operation.EmitData(op, data)
}

// OnFinish registers an event listener that will be called when the operation
// finishes.
func OnFinish[O Operation, E operation.ResultOf[O]](op Operation, l operation.EventListener[O, E]) {
	operation.OnFinish(op, l)
}
