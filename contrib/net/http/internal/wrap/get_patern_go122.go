// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build go1.22 && !go1.23

package wrap

import (
	"net/http"
)

// getPattern returns the pattern associated with the request or the route if no wildcard is used
func getPattern(mux *http.ServeMux, r *http.Request) string {
	if mux == nil { // Will not be available if the user uses WrapHandler
		return ""
	}

	_, pattern := mux.Handler(r)
	return pattern
}
