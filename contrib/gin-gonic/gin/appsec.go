// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gin

import (
	"net/http"

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
	httpsec.WrapHandler(httpWrapper, span, params, &httpsec.Config{
		Framework:       "github.com/gin-gonic/gin",
		OnBlock:         []func(){func() { c.Abort() }},
		RouteForRequest: func(*http.Request) string { return c.FullPath() },
	}).ServeHTTP(c.Writer, c.Request)
}
