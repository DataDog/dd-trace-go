// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelc

import (
	"fmt"
	"net/http"

	"github.com/open-telemetry/opentelemetry-go-compile-instrumentation/pkg/hook"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

// afterHandleKey stashes the afterHandle func returned by httptrace.BeforeHandle
// so AfterServeHTTP can run it.
const afterHandleKey = "dd.afterHandle"

// BeforeServeHTTP starts a server span for the incoming request and swaps in the
// traced response writer and request. It is injected at the top of
// (serverHandler).ServeHTTP; the (unexported) receiver is parameter 0, the
// http.ResponseWriter is parameter 1 and the *http.Request is parameter 2.
func BeforeServeHTTP(ictx hook.HookContext, _ any, w http.ResponseWriter, r *http.Request) {
	tw, tr, afterHandle, handled := httptrace.BeforeHandle(defaultServeConfig(r), w, r)
	ictx.SetParam(1, tw)
	ictx.SetParam(2, tr)
	ictx.SetKeyData(afterHandleKey, afterHandle)
	if handled {
		// AppSec short-circuited the request (e.g. blocked); skip the handler.
		ictx.SetSkipCall(true)
	}
}

// AfterServeHTTP finishes the server span started by BeforeServeHTTP.
func AfterServeHTTP(ictx hook.HookContext) {
	if afterHandle, ok := ictx.GetKeyData(afterHandleKey).(func()); ok && afterHandle != nil {
		afterHandle()
	}
}

// defaultServeConfig builds the server tracing configuration, mirroring the
// resource naming used by contrib/net/http/v2/internal/orchestrion's handler
// wrapper. A nil IsStatusError lets httptrace apply its own default derived from
// DD_TRACE_HTTP_SERVER_ERROR_STATUSES.
func defaultServeConfig(r *http.Request) *httptrace.ServeConfig {
	resource := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
	if options.GetBoolEnv("DD_TRACE_HTTP_HANDLER_RESOURCE_NAME_QUANTIZE", false) {
		resource = fmt.Sprintf("%s %s", r.Method, httptrace.QuantizeURL(r.URL.Path))
	}
	return &httptrace.ServeConfig{
		Resource: resource,
	}
}
