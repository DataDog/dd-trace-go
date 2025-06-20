// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httprouter provides functions to trace the [github.com/julienschmidt/httprouter] package.
package httprouter // import "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2"

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/julienschmidt/httprouter"

	"github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2/internal/tracing"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageJulienschmidtHTTPRouter)
}

// Router is a traced version of [httprouter.Router].
type Router struct {
	*httprouter.Router
	config *tracing.Config
}

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
	cfg := tracing.NewConfig(opts...)
	instr.Logger().Debug("contrib/julienschmidt/httprouter: Configuring Router: %#v", cfg)
	return &Router{httprouter.New(), cfg}
}

// ServeHTTP implements [http.Handler].
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	tw, treq, afterHandle, handled := tracing.BeforeHandle(r.config, r.Router, wrapRouter, w, req)
	defer afterHandle()
	if handled {
		return
	}
	r.Router.ServeHTTP(tw, treq)
}

type wRouter struct {
	*httprouter.Router
}

func wrapRouter(r *httprouter.Router) tracing.Router {
	return &wRouter{r}
}

func (w wRouter) Lookup(method string, path string) (any, []tracing.Param, bool) {
	h, params, ok := w.Router.Lookup(method, path)
	return h, wrapParams(params), ok
}

type wParam struct {
	httprouter.Param
}

func wrapParams(params httprouter.Params) []tracing.Param {
	wParams := make([]tracing.Param, len(params))
	for i, p := range params {
		wParams[i] = wParam{p}
	}
	return wParams
}

func (w wParam) GetKey() string {
	return w.Key
}

func (w wParam) GetValue() string {
	return w.Value
}
