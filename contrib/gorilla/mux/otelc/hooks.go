// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for github.com/gorilla/mux.
// See otelc.yaml. It is a separate module so otelc never reaches contrib users
// who do not use it.
package otelc

import (
	"net/http"
	"sync"
	"weak"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/otelc/pkg/hook"

	muxtrace "github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2"
)

// routers holds the contrib router that traces each mux.Router, built the
// first time the router serves a request. Weak keys let a router be freed once
// nothing else uses it.
var routers sync.Map // weak.Pointer[mux.Router] -> *muxtrace.Router

// BeforeServeHTTP serves the request through the contrib, which traces it, and
// skips the original call. The contrib calls ServeHTTP back with the request
// marked for this router, and that call goes through unchanged. A router the
// application wrapped with the contrib itself is marked the same way, so it is
// traced once.
func BeforeServeHTTP(ictx hook.HookContext, r *mux.Router, w http.ResponseWriter, req *http.Request) {
	if r == nil || req == nil || muxtrace.TraceMarked(req.Context(), r) {
		return
	}
	key := weak.Make(r)
	traced, ok := routers.Load(key)
	if !ok {
		traced, _ = routers.LoadOrStore(key, muxtrace.WrapRouter(r))
	}
	traced.(*muxtrace.Router).ServeHTTP(w, req)
	ictx.SetSkipCall(true)
}
