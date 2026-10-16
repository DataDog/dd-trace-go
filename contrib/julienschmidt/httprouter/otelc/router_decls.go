// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build ignore

// This file is never compiled as part of this package. The add_file rule in
// otelc.yaml copies it into github.com/julienschmidt/httprouter during an otelc
// build, where Router, Params and the injected __dd_config field exist. otelc
// rewrites the package clause and drops the build constraint on the way in.
//
// It compiles under httprouter's go directive (go 1.7), so it cannot use
// generics or any.
package otelc

import (
	tracing "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2/internal/tracing"
)

type __dd_wRouter struct {
	*Router
}

func (w __dd_wRouter) Lookup(method string, path string) (interface{}, []tracing.Param, bool) {
	h, params, ok := w.Router.Lookup(method, path)
	return h, __dd_wrapParams(params), ok
}

type __dd_wParam struct {
	Param
}

func __dd_wrapParams(params Params) []tracing.Param {
	wParams := make([]tracing.Param, len(params))
	for i, p := range params {
		wParams[i] = __dd_wParam{p}
	}
	return wParams
}

func (w __dd_wParam) GetKey() string {
	return w.Key
}

func (w __dd_wParam) GetValue() string {
	return w.Value
}

func __dd_init(r *Router) {
	if r.__dd_config != nil {
		return
	}
	r.__dd_config = tracing.NewConfig()
}
