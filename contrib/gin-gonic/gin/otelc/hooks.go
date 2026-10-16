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

// Each rule has its own hook. otelc declares a hook's linkname stub once per
// file, so a hook shared by methods that gin keeps in different files would be
// declared twice in the package.

// AfterBind monitors the value bound by [gin.Context.Bind].
func AfterBind(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterShouldBind monitors the value bound by [gin.Context.ShouldBind].
func AfterShouldBind(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterBindJSON monitors the value bound by [gin.Context.BindJSON].
func AfterBindJSON(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterShouldBindJSON monitors the value bound by [gin.Context.ShouldBindJSON].
func AfterShouldBindJSON(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterBindTOML monitors the value bound by [gin.Context.BindTOML].
func AfterBindTOML(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterShouldBindTOML monitors the value bound by [gin.Context.ShouldBindTOML].
func AfterShouldBindTOML(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterBindYAML monitors the value bound by [gin.Context.BindYAML].
func AfterBindYAML(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterShouldBindYAML monitors the value bound by [gin.Context.ShouldBindYAML].
func AfterShouldBindYAML(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterBindXML monitors the value bound by [gin.Context.BindXML].
func AfterBindXML(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// AfterShouldBindXML monitors the value bound by [gin.Context.ShouldBindXML].
func AfterShouldBindXML(ictx hook.HookContext, err error) { monitorParsedBody(ictx, err) }

// BeforeASCIIJSON monitors the response body passed to [gin.Context.AsciiJSON].
func BeforeASCIIJSON(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeIndentedJSON monitors the response body passed to [gin.Context.IndentedJSON].
func BeforeIndentedJSON(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeJSON monitors the response body passed to [gin.Context.JSON].
func BeforeJSON(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeJSONP monitors the response body passed to [gin.Context.JSONP].
func BeforeJSONP(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforePureJSON monitors the response body passed to [gin.Context.PureJSON].
func BeforePureJSON(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeSecureJSON monitors the response body passed to [gin.Context.SecureJSON].
func BeforeSecureJSON(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeXML monitors the response body passed to [gin.Context.XML].
func BeforeXML(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeYAML monitors the response body passed to [gin.Context.YAML].
func BeforeYAML(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeTOML monitors the response body passed to [gin.Context.TOML].
func BeforeTOML(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
	monitorResponseBody(ictx, c, obj)
}

// BeforeProtoBuf monitors the response body passed to [gin.Context.ProtoBuf].
func BeforeProtoBuf(ictx hook.HookContext, c *gin.Context, _ int, obj any) {
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
