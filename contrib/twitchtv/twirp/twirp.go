// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package twirp provides tracing functions for tracing clients and servers generated
// by the twirp framework (https://github.com/twitchtv/twirp).
package twirp // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/twitchtv/twirp"

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/twitchtv/twirp"
)

type contextKey int

const (
	twirpErrorKey contextKey = iota
)

// HTTPClient is duplicated from twirp's generated service code.
// It is declared in this package so that the client can be wrapped
// to initiate traces.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type wrappedClient struct {
	c   HTTPClient
	cfg *config
}

// WrapClient wraps a HTTPClient to add disributed tracing to its requests.
func WrapClient(c HTTPClient, opts ...Option) HTTPClient {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &wrappedClient{c: c, cfg: cfg}
}

func (wc *wrappedClient) Do(req *http.Request) (*http.Response, error) {
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.Tag(ext.HTTPMethod, req.Method),
		tracer.Tag(ext.HTTPURL, req.URL.Path),
	}
	if wc.cfg.serviceName != "" {
		opts = append(opts, tracer.ServiceName(wc.cfg.serviceName))
	}
	ctx := req.Context()
	if pkg, ok := twirp.PackageName(ctx); ok {
		opts = append(opts, tracer.Tag("twirp.package", pkg))
	} else {
		fmt.Println("no package")
	}
	if svc, ok := twirp.ServiceName(ctx); ok {
		opts = append(opts, tracer.Tag("twirp.service", svc))
	}
	if method, ok := twirp.MethodName(ctx); ok {
		opts = append(opts, tracer.Tag("twirp.method", method))
	}
	if !math.IsNaN(wc.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, wc.cfg.analyticsRate))
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}

	span, ctx := tracer.StartSpanFromContext(req.Context(), "twirp.request", opts...)
	defer span.Finish()

	req = req.WithContext(ctx)
	err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header))
	if err != nil {
		fmt.Fprintf(os.Stderr, "contrib/twitchtv/twirp.wrappedClient: failed to inject http headers: %v\n", err)
	}

	res, err := wc.c.Do(req)
	if err != nil {
		span.SetTag(ext.Error, err)
	} else {
		span.SetTag(ext.HTTPCode, strconv.Itoa(res.StatusCode))
		// treat 4XX and 5XX as errors for a client
		if res.StatusCode >= 400 && res.StatusCode < 600 {
			span.SetTag(ext.Error, fmt.Errorf("%s", res.Status))
		}
	}
	return res, err
}

// WrapServer wraps a http.Handler to add distributed tracing to a Twirp server.
func WrapServer(h http.Handler, opts ...Option) http.Handler {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := []tracer.StartSpanOption{
			tracer.SpanType(ext.SpanTypeWeb),
			tracer.Tag(ext.HTTPMethod, r.Method),
			tracer.Tag(ext.HTTPURL, r.URL.Path),
		}
		if cfg.serviceName != "" {
			opts = append(opts, tracer.ServiceName(cfg.serviceName))
		}
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
			opts = append(opts, tracer.ChildOf(spanctx))
		}

		span, ctx := tracer.StartSpanFromContext(r.Context(), "twirp.request", opts...)
		defer span.Finish()

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
		opts := []tracer.StartSpanOption{
			tracer.SpanType(ext.SpanTypeWeb),
		}
		if cfg.serviceName != "" {
			opts = append(opts, tracer.ServiceName(cfg.serviceName))
		}
		if pkg, ok := twirp.PackageName(ctx); ok {
			opts = append(opts, tracer.Tag("twirp.package", pkg))
		}
		if svc, ok := twirp.ServiceName(ctx); ok {
			opts = append(opts, tracer.Tag("twirp.service", svc))
		}
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
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
		if method, ok := twirp.MethodName(ctx); ok {
			span.SetTag("twirp.method", method)
		}
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
