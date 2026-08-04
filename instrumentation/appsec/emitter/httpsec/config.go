// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"net/http"
	"net/netip"
)

type Config struct {
	// Framework is the name of the framework or library being used (optional).
	Framework string
	// RemoteIP and ClientIP are the client identity, already resolved by the
	// caller. They are set by integrations that determine identity themselves
	// rather than by scanning the request headers. ClientIP is the switch: when
	// it is invalid the default resolution policy runs and produces both values,
	// and RemoteIP is ignored.
	// When ClientIP is valid, RemoteIP is taken exactly as given, so leaving it
	// invalid means no network.client.ip tag is reported for the request.
	RemoteIP netip.Addr
	ClientIP netip.Addr
	// OnBlock is a list of callbacks to be invoked when a block decision is made.
	OnBlock []func()
	// ResponseHeaderCopier provides a way to access response headers for reading
	// purposes (the value may be provided by copy). This allows customers to
	// apply synchronization if they allow http.ResponseWriter objects to be
	// accessed by multiple goroutines.
	ResponseHeaderCopier func(http.ResponseWriter) http.Header
	// Route is the route name to be used for the request.
	Route string
	// RouteParams is a map of route parameters to be used for the request.
	RouteParams map[string]string
}

var defaultWrapHandlerConfig = &Config{
	ResponseHeaderCopier: func(w http.ResponseWriter) http.Header { return w.Header() },
}
