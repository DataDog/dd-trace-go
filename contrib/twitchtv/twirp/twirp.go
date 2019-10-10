// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package twirp

import (
	"context"
	"math"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/twitchtv/twirp"
)

type contextKey int

const (
	twirpErrorKey contextKey = 0
	httpSpanKey   contextKey = 1
)

// WrapServer wraps a http.Handler to add distributed tracing to a Twirp server.
func WrapServer(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := []tracer.StartSpanOption{
			tracer.SpanType(ext.SpanTypeHTTP),
			tracer.Tag(ext.HTTPMethod, r.Method),
			tracer.Tag(ext.HTTPURL, r.URL.String()),
		}
		if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
			opts = append(opts, tracer.ChildOf(spanctx))
		}

		span, ctx := tracer.StartSpanFromContext(r.Context(), "twirp.request", opts...)
		defer span.Finish()

		ctx = context.WithValue(ctx, httpSpanKey, span)
		r = r.WithContext(ctx)
		h.ServeHTTP(w, r)
	})
}

// NewServerHooks creates the callback hooks for a twirp server to perform tracing.
func NewServerHooks(opts ...Option) *twirp.ServerHooks {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &twirp.ServerHooks{
		RequestReceived:  requestReceivedHook(cfg),
		RequestRouted:    requestRoutedHook(cfg),
		ResponsePrepared: responsePreparedHook(cfg),
		ResponseSent:     responseSentHook(cfg),
		Error:            errorHook(cfg),
	}
}

func requestReceivedHook(cfg *config) func(context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		pkg, ok := twirp.PackageName(ctx)
		if !ok {
			pkg = "unknown package"
		}
		svc, ok := twirp.ServiceName(ctx)
		if !ok {
			svc = "unknown service"
		}
		name := pkg + "." + svc
		if cfg.serviceName != "" {
			name = cfg.serviceName
		}
		opts := []tracer.StartSpanOption{
			tracer.SpanType(ext.SpanTypeWeb),
			tracer.ServiceName(name),
		}
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		if span, ok := ctx.Value(httpSpanKey).(tracer.Span); ok {
			span.SetTag(ext.ServiceName, name)
		}
		_, ctx = tracer.StartSpanFromContext(ctx, "twirp.request", opts...)
		return ctx, nil
	}
}

func requestRoutedHook(cfg *config) func(context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		span, ok := tracer.SpanFromContext(ctx)
		if !ok {
			return ctx, nil
		}

		method, ok := twirp.MethodName(ctx)
		if !ok {
			return ctx, nil
		}
		span.SetTag(ext.ResourceName, method)
		return ctx, nil
	}
}

func responsePreparedHook(cfg *config) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return ctx
	}
}

func responseSentHook(cfg *config) func(context.Context) {
	return func(ctx context.Context) {
		span, ok := tracer.SpanFromContext(ctx)
		if !ok {
			return
		}

		if sc, ok := twirp.StatusCode(ctx); ok {
			span.SetTag(ext.HTTPCode, sc)
		}

		err, _ := ctx.Value(twirpErrorKey).(twirp.Error)
		span.Finish(tracer.WithError(err))
	}
}

func errorHook(cfg *config) func(context.Context, twirp.Error) context.Context {
	return func(ctx context.Context, err twirp.Error) context.Context {
		return context.WithValue(ctx, twirpErrorKey, err)
	}
}
