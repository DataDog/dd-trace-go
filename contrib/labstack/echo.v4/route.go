// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package echo

import (
	"strings"

	"github.com/labstack/echo/v4"
)

func parseIgnoredRoutes(value string) map[string]struct{} {
	ignoredRoutes := make(map[string]struct{})
	for _, route := range strings.Split(value, ",") {
		fields := strings.Fields(route)
		if len(fields) != 2 {
			continue
		}
		ignoredRoutes[routeKey(strings.ToUpper(fields[0]), fields[1])] = struct{}{}
	}
	if len(ignoredRoutes) == 0 {
		return nil
	}
	return ignoredRoutes
}

func isIgnoredRoute(cfg *config, c echo.Context) bool {
	if cfg == nil || len(cfg.ignoredRoutes) == 0 || c == nil || c.Request() == nil || c.Path() == "" {
		return false
	}
	_, found := cfg.ignoredRoutes[routeKey(c.Request().Method, c.Path())]
	return found
}

func routeKey(method, path string) string {
	return method + "\x00" + path
}
