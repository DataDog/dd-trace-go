// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package echo

import (
	"github.com/labstack/echo/v5"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// OnAddRoute is used as [echo.Echo.OnAddRoute] value to automatically collect
// route information as it is being registered in the router, so they appear in
// the API Catalog even if they receive no traffic.
//
// The collection can be disabled at runtime by setting
// `DD_API_SECURITY_ENDPOINT_COLLECTION_ENABLED` to a false-ey value.
func OnAddRoute(route echo.Route) error {
	if !instr.APISecurityEndpointCollectionEnabled() {
		return nil
	}

	instr.TelemetryRegisterAppEndpoint(
		instr.OperationName(instrumentation.ComponentServer, nil),
		route.Method+" "+route.Path,
		instrumentation.AppEndpointAttributes{
			Kind:     "REST",
			Method:   route.Method,
			Path:     route.Path,
			Metadata: map[string]any{"component": instrumentation.PackageLabstackEchoV5},
		},
	)
	return nil
}
