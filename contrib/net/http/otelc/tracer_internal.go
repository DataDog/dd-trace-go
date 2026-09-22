// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"net/http"
	"reflect"
	"sync"
)

// tracerInternalField is the name of the bool field the
// transport_tracer_internal_field rule adds to net/http.Transport, and that the
// transport_tracer_internal_* rules set to true on every Transport literal
// dd-trace-go writes for its own use.
const tracerInternalField = "DD__tracer_internal"

// tracerInternalIndex resolves the injected field once. It is -1 in a plain
// build, where the field does not exist.
//
// The field has to be read reflectively: a hook lives in its own module and
// compiles against the real net/http, where Transport has no such field, so
// naming it would not compile. Orchestrion reads it directly because its
// equivalent code is injected into net/http itself.
var tracerInternalIndex = sync.OnceValue(func() int {
	field, ok := reflect.TypeFor[http.Transport]().FieldByName(tracerInternalField)
	if !ok || field.Type.Kind() != reflect.Bool || len(field.Index) != 1 {
		return -1
	}
	return field.Index[0]
})

// isTracerInternal reports whether transport belongs to dd-trace-go itself.
// Tracing those would make every flush produce the spans the next flush sends.
func isTracerInternal(transport *http.Transport) bool {
	index := tracerInternalIndex()
	if index < 0 || transport == nil {
		return false
	}
	return reflect.ValueOf(transport).Elem().Field(index).Bool()
}
