// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net/http"
	"testing"
)

type fakeRoutingHandler struct{}

func (fakeRoutingHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestRoutingHandlerRegistry(t *testing.T) {
	RegisterRoutingHandlerType[*fakeRoutingHandler]()

	if !IsRoutingHandler(&fakeRoutingHandler{}) {
		t.Error("IsRoutingHandler(*fakeRoutingHandler) = false, want true")
	}
	if IsRoutingHandler(http.NewServeMux()) {
		t.Error("IsRoutingHandler(*http.ServeMux) = true, want false")
	}
	if IsRoutingHandler(nil) {
		t.Error("IsRoutingHandler(nil) = true, want false")
	}
	// A value receiver is a distinct type from its pointer; it must not match.
	if IsRoutingHandler(fakeRoutingHandler{}) {
		t.Error("IsRoutingHandler(fakeRoutingHandler{}) = true, want false")
	}
}
