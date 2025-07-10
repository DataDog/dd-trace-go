// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gin

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"

	"github.com/gin-gonic/gin"
)

// useAppSec executes the AppSec logic related to the operation start
func useAppSec(c *gin.Context, span trace.TagSetter) {
	var params map[string]string
	if l := len(c.Params); l > 0 {
		params = make(map[string]string, l)
		for _, p := range c.Params {
			params[p.Key] = p.Value
		}
	}
	httpWrapper := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		c.Request = r
		c.Next()
	})
	httpsec.WrapHandler(httpWrapper, span, &httpsec.Config{
		Framework:   "github.com/gin-gonic/gin",
		OnBlock:     []func(){func() { c.Abort() }},
		Route:       c.FullPath(),
		RouteParams: params,
	}).ServeHTTP(c.Writer, c.Request)
}

// AsciiJSON is a wrapper around the [gin.Context.AsciiJSON] method that also performs
// appsec HTTP response body monitoring.
func AsciiJSON(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.AsciiJSON(code, obj)
}

// IndentedJSON is a wrapper around the [gin.Context.IndentedJSON] method that also performs
// appsec HTTP response body monitoring.
func IndentedJSON(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.IndentedJSON(code, obj)
}

// JSON is a wrapper around the [gin.Context.JSON] method that also performs
// appsec HTTP response body monitoring.
func JSON(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.JSON(code, obj)
}

// JSONP is a wrapper around the [gin.Context.JSONP] method that also performs
// appsec HTTP response body monitoring.
func JSONP(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.JSONP(code, obj)
}

// PureJSON is a wrapper around the [gin.Context.PureJSON] method that also performs
// appsec HTTP response body monitoring.
func PureJSON(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.PureJSON(code, obj)
}

// SecureJSON is a wrapper around the [gin.Context.SecureJSON] method that also performs
// appsec HTTP response body monitoring.
func SecureJSON(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.SecureJSON(code, obj)
}

// XML is a wrapper around the [gin.Context.XML] method that also performs
// appsec HTTP response body monitoring.
func XML(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.XML(code, obj)
}

// YAML is a wrapper around the [gin.Context.YAML] method that also performs
// appsec HTTP response body monitoring.
func YAML(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.YAML(code, obj)
}

// TOML is a wrapper around the [gin.Context.TOML] method that also performs
// appsec HTTP response body monitoring.
func TOML(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.TOML(code, obj)
}

// ProtoBuf is a wrapper around the [gin.Context.ProtoBuf] method that also performs
// appsec HTTP response body monitoring.
func ProtoBuf(c *gin.Context, code int, obj any) {
	if err := appsec.MonitorHTTPResponseBody(c.Request.Context(), obj); err != nil {
		instr.Logger().Debug("appsec: monitoring of response body resulted in error: %s", err.Error())
	}
	c.ProtoBuf(code, obj)
}
