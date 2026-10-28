// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for github.com/gin-gonic/gin.
// See otelc.yaml. It is a separate module so otelc never reaches contrib users
// who do not use it.
package otelc

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otelc/pkg/hook"

	"github.com/DataDog/dd-trace-go/v2/appsec"

	_ "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2" // used by the otelc.yaml rules
)

// AfterBind monitors the value bound by a [gin.Context] bind method, such as
// [gin.Context.Bind] or [gin.Context.ShouldBindJSON].
func AfterBind(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// BeforeRender monitors the response body passed to a [gin.Context] render
// method, such as [gin.Context.JSON] or [gin.Context.XML].
func BeforeRender(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// monitorParsedBody runs AppSec on the value a successful bind call filled in,
// and makes the call return the blocking error, if any. The hook context still
// holds the receiver and the bound value after the call.
func monitorParsedBody(ictx hook.HookContext, err error) {
	if err != nil {
		return
	}
	c, _ := ictx.GetParam(0).(*gin.Context)
	if c == nil || c.Request == nil {
		return
	}
	if err := appsec.MonitorParsedHTTPBody(c.Request.Context(), ictx.GetParam(1)); err != nil {
		ictx.SetReturnVal(0, err)
	}
}

// monitorResponseBody runs AppSec on a response body before gin writes it, and
// skips the write when AppSec blocks the request. The AppSec handlers in the
// middleware chain send the blocking response.
func monitorResponseBody(ictx hook.HookContext, c *gin.Context, obj any) {
	if c == nil || c.Request == nil {
		return
	}
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		ictx.SetSkipCall(true)
	}
}
