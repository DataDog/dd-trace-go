// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gin

import (
	"net"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"github.com/gin-gonic/gin"
)

// useAppSec executes the AppSec logic related to the operation start and
// returns the  function to be executed upon finishing the operation
func useAppSec(c *gin.Context, span tracer.Span) func() {
	req := c.Request
	httpsec.SetAppSecTags(span)
	var params map[string]string
	if l := len(c.Params); l > 0 {
		params = make(map[string]string, l)
		for _, p := range c.Params {
			params[p.Key] = p.Value
		}
	}
	args := httpsec.MakeHandlerOperationArgs(req, params)
	ctx, op := httpsec.StartOperation(req.Context(), args)
	c.Request = req.WithContext(ctx)
	return func() {
		events := op.Finish(httpsec.HandlerOperationRes{Status: c.Writer.Status()})
		if len(events) > 0 {
			remoteIP, _, err := net.SplitHostPort(req.RemoteAddr)
			if err != nil {
				remoteIP = req.RemoteAddr
			}
			httpsec.SetSecurityEventTags(span, events, remoteIP, args.Headers, c.Writer.Header())
		}
		httpsec.SetTags(span, op.Metrics())
	}
}
