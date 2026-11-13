// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"errors"
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
	// panicked tells that the handler did not return normally. The response
	// status then does not show the result of the handler.
	panicked bool
	// namerPanicked tells that the resource namer panicked after the handler.
	// The response status then does not show the result of the request.
	namerPanicked bool
}

// errHandlerPanic is the span error when the handler panics. It does not
// contain the panic value, because that value can contain request data or
// credentials.
var errHandlerPanic = errors.New("handler panicked")

// errResourceNamerPanic is the span error when the resource namer panics after
// the handler. For the same reason, it does not contain the panic value.
var errResourceNamerPanic = errors.New("resource namer panicked")

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

// wroteBlock tells that AppSec wrote an early block response into the live
// response. handled alone does not tell this: httpsec also stops the handler
// when the block fails because the response is committed.
func (s *handlerScope) wroteBlock() bool {
	return s.handled && s.appsec != nil && s.appsec.writer.wroteHeader
}

func (s *handlerScope) setResource() {
	s.resource = s.cfg.resourceNamer(s.ctx)
	s.resourceSet = true
}

// setFinalResource sets the resource after the handler. It recovers a panic of
// the resource namer, so that the span can still finish, and returns its value.
// The span then reports an error, not the response status. The caller must
// raise the returned value again only if the handler did not panic: the panic
// of the handler must not be replaced.
func (s *handlerScope) setFinalResource() (namerPanic any) {
	defer func() {
		// Since Go 1.21, panic(nil) recovers a *runtime.PanicNilError, so a
		// nil value always means that the namer returned normally.
		if namerPanic = recover(); namerPanic != nil {
			s.namerPanicked = true
		}
	}()
	s.setResource()
	return nil
}

// raiseNamerPanic raises namerPanic again, unless the handler panicked. In that
// case, the panic of the handler continues and the namer panic is discarded.
func (s *handlerScope) raiseNamerPanic(namerPanic any) {
	if namerPanic != nil && !s.panicked {
		panic(namerPanic)
	}
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
		if s.panicked {
			// The response still has the status that the handler had set
			// before the panic (fasthttp's default is 200). Do not report it.
			s.span.SetTag(ext.ErrorNoStackTrace, errHandlerPanic)
			return
		}
		if s.namerPanicked {
			s.span.SetTag(ext.ErrorNoStackTrace, errResourceNamerPanic)
			return
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
	// A completed timeout layer can have set a resource before the handler
	// ran. Its worker has exited, so set the resource again from the final
	// context.
	// The deferred calls run before the namer panic continues.
	s.raiseNamerPanic(s.setFinalResource())
}

func restoreUserValue(ctx *fasthttp.RequestCtx, key, previous any) {
	if previous == nil {
		ctx.RemoveUserValue(key)
	} else {
		ctx.SetUserValue(key, previous)
	}
}
