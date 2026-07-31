// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelc

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otelc/pkg/hook"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

// BeforeServe ensures srv.Handler is wrapped with contrib/net/http tracing
// before the server starts accepting connections. It is injected at the top
// of (*http.Server).Serve; the receiver is parameter 0.
func BeforeServe(_ hook.HookContext, srv *http.Server) {
	handler := srv.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	srv.Handler = wrapHandler(handler)
}

// wrapHandler mirrors contrib/net/http/v2/internal/orchestrion.WrapHandler
func wrapHandler(handler http.Handler) http.Handler {
	switch handler := handler.(type) {
	case *wrap.ServeMux, wrap.WrappedHandler:
		return handler
	case *http.ServeMux:
		tracedMux := wrap.NewServeMux()
		tracedMux.ServeMux = handler
		return tracedMux
	default:
		if options.GetBoolEnv("DD_TRACE_HTTP_HANDLER_RESOURCE_NAME_QUANTIZE", false) {
			return wrap.Handler(handler, "", "", config.WithResourceNamer(quantizeResourceNamer))
		}
		return wrap.Handler(handler, "", "", config.WithResourceNamer(resourceNamer))
	}
}

func resourceNamer(r *http.Request) string {
	return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
}

func quantizeResourceNamer(r *http.Request) string {
	return fmt.Sprintf("%s %s", r.Method, httptrace.QuantizeURL(r.URL.Path))
}
