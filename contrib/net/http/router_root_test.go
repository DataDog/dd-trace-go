// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type rootSpanRouter struct{}

func (rootSpanRouter) ServeHTTP(http.ResponseWriter, *http.Request) {}

// TestWrapHandlerRouterRootSpan covers the manual (non-Orchestrion) path of the
// DD_TRACE_HTTP_ROUTER_ROOT_SPAN opt-in: a registered router is returned
// unwrapped only when the flag is enabled.
func TestWrapHandlerRouterRootSpan(t *testing.T) {
	httptrace.RegisterRoutingHandlerType[*rootSpanRouter]()
	r := &rootSpanRouter{}

	t.Run("enabled skips wrapping", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_ROUTER_ROOT_SPAN", "true")
		if got := WrapHandler(r, "", ""); got != http.Handler(r) {
			t.Errorf("WrapHandler(registered router) with flag on = %T, want handler unchanged", got)
		}
	})

	t.Run("disabled wraps", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_ROUTER_ROOT_SPAN", "false")
		if got := WrapHandler(r, "", ""); got == http.Handler(r) {
			t.Error("WrapHandler(registered router) with flag off should wrap the handler")
		}
	})
}
