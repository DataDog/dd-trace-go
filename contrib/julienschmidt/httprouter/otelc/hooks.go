// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for
// github.com/julienschmidt/httprouter. See otelc.yaml. It is a separate module
// so otelc never reaches contrib users who do not use it.
package otelc

import (
	"net/http"
	"runtime"
	"sync"
	"weak"

	"github.com/julienschmidt/httprouter"
	"go.opentelemetry.io/otelc/pkg/hook"

	httptrace "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2"
)

// configs holds the contrib configuration of each httprouter.Router, built the
// first time the router serves a request. The values hold no reference to their
// router, and an entry is deleted once its router is freed.
var configs sync.Map // weak.Pointer[httprouter.Router] -> *httptrace.Router with a nil Router

// contribRouter returns the contrib router that traces r.
//
// A call before the contrib's init panics in WrapRouter. The hook trampoline
// recovers it and the request is served untraced. Nothing is stored, so the
// next request builds the configuration again.
func contribRouter(r *httprouter.Router) *httptrace.Router {
	key := weak.Make(r)
	cfg, ok := configs.Load(key)
	if !ok {
		var loaded bool
		cfg, loaded = configs.LoadOrStore(key, httptrace.WrapRouter(nil))
		if !loaded {
			runtime.AddCleanup(r, func(key weak.Pointer[httprouter.Router]) { configs.Delete(key) }, key)
		}
	}
	traced := *cfg.(*httptrace.Router)
	traced.Router = r
	return &traced
}

// BeforeServeHTTP starts the span for a request r serves and passes the traced
// http.ResponseWriter and http.Request on to the original call. The handlers
// run in that call, outside the hook. A router the application wrapped with
// the contrib marks the requests it serves, and they are left alone.
func BeforeServeHTTP(ictx hook.HookContext, r *httprouter.Router, w http.ResponseWriter, req *http.Request) {
	if r == nil || req == nil || httptrace.TraceMarked(req.Context(), r) {
		return
	}
	tw, treq, afterHandle, handled := contribRouter(r).BeforeHandle(w, req)
	ictx.SetData(afterHandle)
	if handled {
		ictx.SetSkipCall(true)
		return
	}
	// Parameter 0 is the receiver.
	ictx.SetParam(1, tw)
	ictx.SetParam(2, treq)
}

// AfterServeHTTP finishes the span BeforeServeHTTP started. It also runs when a
// handler panics, and the panic goes on to the caller.
func AfterServeHTTP(ictx hook.HookContext) {
	if afterHandle, ok := ictx.GetData().(func()); ok {
		afterHandle()
	}
}
