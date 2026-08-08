// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package echo

import (
	"sync"
	"sync/atomic"

	"github.com/labstack/echo/v4"
)

var (
	hasIgnoredRoutes atomic.Bool
	ignoredRoutes    sync.Map
)

// IgnoreRoute records route as ignored by Echo tracing middleware and returns it.
func IgnoreRoute[T *echo.Route | []*echo.Route](route T) T {
	switch route := any(route).(type) {
	case *echo.Route:
		ignoreRoute(route)
	case []*echo.Route:
		for _, route := range route {
			ignoreRoute(route)
		}
	}
	return route
}

func ignoreRoute(route *echo.Route) {
	if route == nil {
		return
	}
	ignoredRoutes.Store(route, struct{}{})
	hasIgnoredRoutes.Store(true)
}

func isIgnoredRoute(c echo.Context) bool {
	if !hasIgnoredRoutes.Load() || c == nil || c.Request() == nil || c.Echo() == nil {
		return false
	}

	path := c.Path()
	if path == "" {
		return false
	}

	request := c.Request()
	if router := c.Echo().Routers()[request.Host]; router != nil {
		return isIgnoredRouteIn(router.Routes(), request.Method, path)
	}

	return isIgnoredRouteIn(c.Echo().Routes(), request.Method, path)
}

func isIgnoredRouteIn(routes []*echo.Route, method, path string) bool {
	for _, route := range routes {
		if route == nil || route.Path != path {
			continue
		}
		if route.Method != method && route.Method != echo.RouteNotFound {
			continue
		}
		if _, found := ignoredRoutes.Load(route); found {
			return true
		}
	}
	return false
}
