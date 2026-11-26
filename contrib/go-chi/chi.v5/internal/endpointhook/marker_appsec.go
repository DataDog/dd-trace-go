// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package endpointhook provides the endpoint hook used by woven chi builds.
// The chi contrib must import this package so otelc can link the hook.
package endpointhook

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
)

type Reporter interface {
	ReportedRoute(*chi.Context) string
}

type Marker struct {
	Disabled bool
	Depth    int
	Route    Reporter
}

type markerKey struct{}

type markerContext struct {
	context.Context
	marker Marker
}

func (c *markerContext) Value(key any) any {
	if _, ok := key.(markerKey); ok {
		return &c.marker
	}
	return c.Context.Value(key)
}

func With(ctx context.Context, marker Marker) context.Context {
	return &markerContext{Context: ctx, marker: marker}
}

func Woven() bool { return woven.Load() }

var routeMatched = httpsec.RouteMatched

// SetWovenForTest must not be installed or restored while requests run.
func SetWovenForTest(value bool) func() {
	old := woven.Swap(value)
	return func() { woven.Store(old) }
}

// SetRouteMatchedForTest must not be installed or restored while requests run.
func SetRouteMatchedForTest(fn func(context.Context, string, map[string]string) error) func() {
	old := routeMatched
	routeMatched = fn
	return func() { routeMatched = old }
}
