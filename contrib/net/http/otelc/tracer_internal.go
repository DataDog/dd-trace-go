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
//
// FIXME: this returns false in an application that imports nothing from
// dd-trace-go, which is the zero-touch case otelc exists for. otelc plans which
// packages to instrument from a dry run of the uninstrumented build, and only
// then generates the otelc.runtime.go that pulls dd-trace-go in, so the rules
// that mark these transports never run and the flush loop above is real. The
// foundation's GLS rules and enable_otelc_flag are skipped for the same reason.
// Not fixable here; it needs otelc to plan after injection.
func isTracerInternal(transport *http.Transport) bool {
	index := tracerInternalIndex()
	if index < 0 || transport == nil {
		return false
	}
	return reflect.ValueOf(transport).Elem().Field(index).Bool()
}
