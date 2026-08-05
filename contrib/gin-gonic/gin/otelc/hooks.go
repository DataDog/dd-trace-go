// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks that reproduce
// ../orchestrion.yml for an otelc-instrumented build. It is its own module so
// that importing go.opentelemetry.io/otelc/pkg/hook does not leak into the
// go.mod of everyone who imports the gin contrib.
//
// The middleware-injection aspect (see otelc.yaml, id new_*) needs no Go code:
// it is a pure wrap_call rewrite at the gin.New/gin.Default call site, calling
// the contrib's exported Middleware directly from the generated code.
package otelc

import (
	"github.com/gin-gonic/gin"

	"github.com/DataDog/dd-trace-go/v2/appsec"

	"go.opentelemetry.io/otelc/pkg/hook"
)

// AfterBind reproduces the gin.Context.[Must]Bind aspect: after a successful
// bind, run the AppSec request-body monitor on the bound value and surface a
// resulting error the same way the original bind error would have surfaced.
//
// Bind is an after-hook, so the receiver and the bound argument are not typed
// Go parameters; they are read back from the hook context, which still holds
// them regardless of hook direction.
func AfterBind(ctx hook.HookContext, err error) {
	if err != nil {
		return
	}
	c, ok := ctx.GetParam(0).(*gin.Context)
	if !ok {
		return
	}
	obj := ctx.GetParam(1)
	if monitorErr := appsec.MonitorParsedHTTPBody(c.Request.Context(), obj); monitorErr != nil {
		ctx.SetReturnVal(0, monitorErr)
	}
}

// BeforeResponseBody reproduces the Response.Body aspect: run the AppSec
// response-body monitor before gin writes the response body, and skip the
// original call when AppSec blocks the request. The blocking response itself
// is sent by AppSec's own middleware handlers further up the chain.
func BeforeResponseBody(ctx hook.HookContext, recv any, _ int, obj any) {
	c, ok := recv.(*gin.Context)
	if !ok {
		return
	}
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		ctx.SetSkipCall(true)
	}
}
