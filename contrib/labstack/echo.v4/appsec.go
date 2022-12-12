// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"github.com/labstack/echo/v4"
)

func useAppSec(c echo.Context, span tracer.Span) func() {
	instrumentation.SetAppSecEnabledTags(span)

	params := make(map[string]string)
	for _, n := range c.ParamNames() {
		params[n] = c.Param(n)
	}

	req := c.Request()
	ipTags, clientIP := httpsec.ClientIPTags(req.Header, req.RemoteAddr)
	instrumentation.SetStringTags(span, ipTags)

	args := httpsec.MakeHandlerOperationArgs(req, clientIP, params)
	ctx, op := httpsec.StartOperation(req.Context(), args)
	c.SetRequest(req.WithContext(ctx))
	return func() {
		events := op.Finish(httpsec.HandlerOperationRes{Status: c.Response().Status})
		if len(events) > 0 {
			httpsec.SetSecurityEventTags(span, events, args.Headers, c.Response().Writer.Header())
		}
		instrumentation.SetTags(span, op.Tags())
	}
}
