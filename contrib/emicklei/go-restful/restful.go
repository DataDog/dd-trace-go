// Package restful provides functions to trace the emicklei/go-restful package (https://github.com/emicklei/go-restful).
package restful

import (
	"strconv"

	"github.com/emicklei/go-restful"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// FilterFunc returns a restful.FilterFunction which will automatically trace incoming request.
func FilterFunc(configOpts ...Option) restful.FilterFunction {
	cfg := newConfig()
	for _, opt := range configOpts {
		opt(cfg)
	}
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		opts := []ddtrace.StartSpanOption{
			tracer.ServiceName(cfg.serviceName),
			tracer.ResourceName(req.SelectedRoutePath()),
			tracer.SpanType(ext.SpanTypeWeb),
			tracer.Tag(ext.HTTPMethod, req.Request.Method),
			tracer.Tag(ext.HTTPURL, req.Request.URL.Path),
		}
		if cfg.analyticsRate > 0 {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Request.Header)); err == nil {
			opts = append(opts, tracer.ChildOf(spanctx))
		}
		span, ctx := tracer.StartSpanFromContext(req.Request.Context(), "http.request", opts...)
		defer span.Finish()

		// pass the span through the request context
		req.Request = req.Request.WithContext(ctx)

		chain.ProcessFilter(req, resp)

		span.SetTag(ext.HTTPCode, strconv.Itoa(resp.StatusCode()))
		span.SetTag(ext.Error, resp.Error())
	}
}

// Filter is deprecated. Please use FilterFunc.
func Filter(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
	opts := []ddtrace.StartSpanOption{
		tracer.ResourceName(req.SelectedRoutePath()),
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, req.Request.Method),
		tracer.Tag(ext.HTTPURL, req.Request.URL.Path),
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Request.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, ctx := tracer.StartSpanFromContext(req.Request.Context(), "http.request", opts...)
	defer span.Finish()

	// pass the span through the request context
	req.Request = req.Request.WithContext(ctx)

	chain.ProcessFilter(req, resp)

	span.SetTag(ext.HTTPCode, strconv.Itoa(resp.StatusCode()))
	span.SetTag(ext.Error, resp.Error())
}
