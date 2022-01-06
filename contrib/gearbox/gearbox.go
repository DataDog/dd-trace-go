// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package gearbox

import (
	"context"
	"net/http"

	"github.com/gogearbox/gearbox"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Datadog(ctx gearbox.Context) {

	method := string(ctx.Context().Method())

	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, method),
		tracer.Tag(ext.HTTPURL, ctx.Context().URI().String()),
		tracer.Measured(),
	}

	headers := mapCtxToHttpHeader(ctx)

	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(headers)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}

	span, ctxSpan := tracer.StartSpanFromContext(context.Background(), "http.request", opts...)

	ctx.SetLocal("ctxspan", ctxSpan)
	ctx.Next()

	span.SetTag(ext.ResourceName, string(ctx.Context().URI().Path()))

	status := ctx.Context().Response.StatusCode()

	span.SetTag(ext.HTTPCode, status)

	if status == gearbox.StatusInternalServerError || status == gearbox.StatusBadRequest {

		b := string(ctx.Context().Response.Body())
		span.SetTag(ext.Error, true)
		span.SetTag(ext.ErrorMsg, b)
	}

	span.Finish()

}

func mapCtxToHttpHeader(ctx gearbox.Context) http.Header {

	headers := http.Header{}

	listHeadersDatadog := [3]string{
		"X-Datadog-Trace-Id",
		"X-Datadog-Parent-Id",
		"X-Datadog-Sampling-Priority",
	}

	for _, headerDataDog := range listHeadersDatadog {

		valueHeader := ctx.Get(headerDataDog)

		if len(valueHeader) > 0 {
			headers.Add(headerDataDog, valueHeader)
		}
	}

	return headers

}
