// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"net"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"github.com/labstack/echo/v4"
)

func useAppSec(c echo.Context, span tracer.Span) func() {
	req := c.Request()
	httpsec.SetAppSecTags(span)
	params := make(map[string]string)
	for _, n := range c.ParamNames() {
		params[n] = c.Param(n)
	}
	args := httpsec.MakeHandlerOperationArgs(req, params)
	ctx, op := httpsec.StartOperation(req.Context(), args)
	c.SetRequest(req.WithContext(ctx))
	return func() {
		events := op.Finish(httpsec.HandlerOperationRes{Status: c.Response().Status})
		if len(events) > 0 {
			remoteIP, _, err := net.SplitHostPort(req.RemoteAddr)
			if err != nil {
				remoteIP = req.RemoteAddr
			}
			httpsec.SetSecurityEventTags(span, events, remoteIP, args.Headers, c.Response().Writer.Header())
		}
		httpsec.SetTags(span, op.Metrics())
	}
}
