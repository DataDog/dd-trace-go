// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package instrumentation

import "reflect"

type Operation interface {
	On(EventListener)
	Register(...EventListener) UnregisterFunc
}

type EventListener interface {
	ListenedType() reflect.Type
	Call(op Operation, v interface{})
}

// UnregisterFunc is a function allowing to unregister from an operation the previously registered event listeners.
type UnregisterFunc func()
