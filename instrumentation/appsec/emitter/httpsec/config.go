// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/net/http/pattern"
)

type Config struct {
	// Framework is the name of the framework or library being used (optional).
	Framework string
	// OnBlock is a list of callbacks to be invoked when a block decision is made.
	OnBlock []func()
	// ResponseHeaderCopier provides a way to access response headers for reading
	// purposes (the value may be provided by copy). This allows customers to
	// apply synchronization if they allow http.ResponseWriter objects to be
	// accessed by multiple goroutines.
	ResponseHeaderCopier func(http.ResponseWriter) http.Header
	// RouteForRequest returns the route string for the given request, or blank if
	// no route information can be determined.
	RouteForRequest func(*http.Request) string
}

var defaultWrapHandlerConfig = &Config{
	ResponseHeaderCopier: func(w http.ResponseWriter) http.Header { return w.Header() },
	RouteForRequest:      func(r *http.Request) string { return pattern.Route(r.Pattern) },
}
