// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gearbox provides tracing functions for APM
package gearbox

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gogearbox/gearbox"
)

const (
	operationName = "http.request"
)

//Datadog this method should implement at a middleware layer
func Middleware(ctx gearbox.Context) {
	method := string(ctx.Context().Method())
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, method),
		tracer.Tag(ext.HTTPURL, ctx.Context().URI().String()),
		tracer.Measured(),
	}

	carrier := gearboxContextCarrier{ctx}
	if spanctx, err := tracer.Extract(carrier); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}

	span, ctxSpan := tracer.StartSpanFromContext(context.Background(), operationName, opts...)
	ctx.SetLocal("ctxspan", ctxSpan)
	//Next function is used to successfully pass from current middleware to next middleware.
	ctx.Next()
	status := ctx.Context().Response.StatusCode()
	resouceName := string(ctx.Context().URI().Path())
	span.SetTag(ext.ResourceName, resouceName)
	span.SetTag(ext.HTTPCode, status)
	if status == gearbox.StatusInternalServerError || status == gearbox.StatusBadRequest {
		span.SetTag(ext.Error, string(ctx.Context().Response.Body()[:5000]))
	}
	span.Finish()
}
