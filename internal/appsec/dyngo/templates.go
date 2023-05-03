// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import "reflect"

// EventListenerTemplate is a generic helper type definition allowing to define
// a dyngo event listener by implementing the EventListener interface type
// properly. A dyngo event listener is defined by its listened operation
// arguments or results type T, along with the corresponding operation O.
// For example, the following type definition of OnOperationStart defines a
// function type func(*Operation, *OperationArgs) implementing the EventListener
// interface expected by dyngo's event registry:
//
//	type OnOperationStart = dyngo.EventListenerTemplate[*Operation, *OperationArgs]
type EventListenerTemplate[O Operation, T any] func(O, T)

func (EventListenerTemplate[_, T]) ListenedType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

func (f EventListenerTemplate[O, T]) Call(op Operation, v any) {
	f(op.(O), v.(T))
}
