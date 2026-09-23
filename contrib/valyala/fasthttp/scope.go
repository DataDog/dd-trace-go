// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type handlerScopeKey struct{}

type handlerScope struct {
	ctx          *fasthttp.RequestCtx
	cfg          *config
	span         *tracer.Span
	appsec       *appsecHandler
	parent       *handlerScope
	previousSpan any
	layer        *timeoutLayer
	handled      bool
	resource     string
	resourceSet  bool
	appsecOnce   sync.Once
	spanOnce     sync.Once
	restoreOnce  sync.Once
}

func startHandlerScope(ctx *fasthttp.RequestCtx, cfg *config) *handlerScope {
	parent, _ := ctx.UserValue(handlerScopeKey{}).(*handlerScope)
	scope := &handlerScope{
		ctx:          ctx,
		cfg:          cfg,
		parent:       parent,
		previousSpan: ctx.UserValue(instr.ActiveSpanKey()),
	}
	spanOpts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
	}
	spanOpts = append(spanOpts, defaultSpanOptions(ctx)...)
	carrier := &HTTPHeadersCarrier{ReqHeader: &ctx.Request.Header}
	if sctx, err := tracer.Extract(carrier); err == nil {
		if sctx != nil && sctx.SpanLinks() != nil {
			spanOpts = append(spanOpts, tracer.WithSpanLinks(sctx.SpanLinks()))
		}
		spanOpts = append(spanOpts, tracer.ChildOf(sctx))
	}
	scope.span = StartSpanFromContext(ctx, "http.request", spanOpts...)
	ctx.SetUserValue(handlerScopeKey{}, scope)
	if instr.AppSecEnabled() {
		scope.appsec, scope.handled = beforeHandle(ctx, scope.span)
	}
	return scope
}

func (s *handlerScope) setResource() {
	s.resource = s.cfg.resourceNamer(s.ctx)
	s.resourceSet = true
}

func (s *handlerScope) finishAppSec(response *fasthttp.Response) {
	s.appsecOnce.Do(func() {
		if s.appsec != nil {
			s.appsec.writer.response = response
			s.appsec.finish()
		}
	})
}

func (s *handlerScope) finishSpan(response *fasthttp.Response) {
	s.spanOnce.Do(func() {
		defer s.span.Finish()
		if s.resourceSet {
			s.span.SetTag(ext.ResourceName, s.resource)
		}
		status := response.StatusCode()
		if s.cfg.isStatusError(status) {
			s.span.SetTag(ext.ErrorNoStackTrace, fmt.Errorf("%d: %s", status, fasthttp.StatusMessage(status)))
		}
		s.span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	})
}

func (s *handlerScope) restore() {
	s.restoreOnce.Do(func() {
		if s.appsec != nil {
			s.appsec.restore()
		}
		restoreUserValue(s.ctx, instr.ActiveSpanKey(), s.previousSpan)
		if s.parent == nil {
			s.ctx.RemoveUserValue(handlerScopeKey{})
		} else {
			s.ctx.SetUserValue(handlerScopeKey{}, s.parent)
		}
	})
}

func (s *handlerScope) finish() {
	if s.layer != nil {
		s.layer.finishOuter(s)
		return
	}
	defer s.restore()
	defer s.finishSpan(&s.ctx.Response)
	defer s.finishAppSec(&s.ctx.Response)
	if !s.resourceSet {
		s.setResource()
	}
}

func restoreUserValue(ctx *fasthttp.RequestCtx, key, previous any) {
	if previous == nil {
		ctx.RemoveUserValue(key)
	} else {
		ctx.SetUserValue(key, previous)
	}
}
