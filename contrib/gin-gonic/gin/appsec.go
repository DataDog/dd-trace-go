// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gin

import (
	"github.com/gin-gonic/gin"
	"net"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
)

func useAppSec(c *gin.Context) {
	req := c.Request
	span, ok := tracer.SpanFromContext(req.Context())
	if ok {
		httpsec.SetAppSecTags(span)
		params := make(map[string]string)
		for _, p := range c.Params {
			params[p.Key] = p.Value
		}
		args := httpsec.MakeHandlerOperationArgs(req, params)
		op := httpsec.StartOperation(args, nil)
		defer func() {
			events := op.Finish(httpsec.HandlerOperationRes{Status: c.Writer.Status()})
			if len(events) > 0 {
				remoteIP, _, err := net.SplitHostPort(req.RemoteAddr)
				if err != nil {
					remoteIP = req.RemoteAddr
				}
				httpsec.SetSecurityEventTags(span, events, remoteIP, args.Headers, c.Writer.Header())
			}
		}()
	}
	c.Next()
}
