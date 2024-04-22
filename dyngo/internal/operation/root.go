// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package operation

import (
	"go.uber.org/atomic"
)

// Atomic *Operation so we can atomically read or swap it.
var rootOperation atomic.Pointer[Operation]

// NewRoot creates and returns a new root operation, with no parent
// operation. Root operations are meant to be the top-level operation of an
// operation stack, therefore receiving all the operation events. It allows to
// prepare a new set of event listeners, to then atomically swap it with the
// current one.
func NewRoot() Operation {
	return &operation{parent: nil}
}

// CurrentRoot returns the current root api.Operation at time of calling.
func CurrentRoot() Operation {
	if op := rootOperation.Load(); op != nil {
		return *op
	}
	return nil
}

// SwapRoot allows to atomically swap the current root operation with
// the given new one. Concurrent uses of the old root operation on already
// existing and running operation are still valid.
func SwapRoot(new Operation) {
	rootOperation.Swap(&new)
	// Note: calling Finish(old, ...) could result into mem leaks because
	// some finish event listeners, possibly releasing memory and resources,
	// wouldn't be called anymore (because Finish() disables the operation and
	// removes the event listeners).
}
