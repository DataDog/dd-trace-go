// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gin

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"

	"github.com/gin-gonic/gin"
)

// useAppSec executes the AppSec logic related to the operation start and
// returns the  function to be executed upon finishing the operation
func useAppSec(c *gin.Context, span tracer.Span) {
	var params map[string]string
	if l := len(c.Params); l > 0 {
		params = make(map[string]string, l)
		for _, p := range c.Params {
			params[p.Key] = p.Value
		}
	}
	httpWrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.Request = r
		c.Next()
	})
	httpsec.WrapHandler(httpWrapper, span, params, &httpsec.Config{
		OnBlock: []func(){func() { c.Abort() }},
	}).ServeHTTP(c.Writer, c.Request)
}
