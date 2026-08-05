// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package echo

import (
	"sync"

	"github.com/labstack/echo/v4"
)

var ignoredRoutes sync.Map

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
	ignoredRoutes.Store(routeKey(route.Method, route.Path), struct{}{})
}

func isIgnoredRoute(c echo.Context) bool {
	if c == nil || c.Request() == nil {
		return false
	}
	path := c.Path()
	if path == "" {
		return false
	}
	if _, found := ignoredRoutes.Load(routeKey(c.Request().Method, path)); found {
		return true
	}
	_, found := ignoredRoutes.Load(routeKey(echo.RouteNotFound, path))
	return found
}

func routeKey(method, path string) string {
	return method + "\x00" + path
}
