// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package orchestrion

import (
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type routerHandler struct{}

func (routerHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

type plainHandler struct{}

func (plainHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestWrapHandlerRegisteredRouter(t *testing.T) {
	httptrace.RegisterRoutingHandlerType[*routerHandler]()

	t.Run("enabled skips wrapping", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_ROUTER_ROOT_SPAN", "true")
		router := &routerHandler{}
		if got := WrapHandler(router); got != http.Handler(router) {
			t.Errorf("WrapHandler(registered router) = %T, want the handler returned unchanged", got)
		}
	})

	t.Run("disabled wraps as usual", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_ROUTER_ROOT_SPAN", "false")
		if _, ok := WrapHandler(&routerHandler{}).(wrap.WrappedHandler); !ok {
			t.Error("WrapHandler with flag off should still wrap a registered router")
		}
	})

	t.Run("unregistered handler always wrapped", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_ROUTER_ROOT_SPAN", "true")
		if _, ok := WrapHandler(&plainHandler{}).(wrap.WrappedHandler); !ok {
			t.Error("WrapHandler(unregistered handler) should return a wrapped handler")
		}
	})
}
