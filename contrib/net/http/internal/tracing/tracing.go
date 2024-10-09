package tracing

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig struct {
	// Service specifies the service name to use. If left blank, the global service name
	// will be inherited.
	Service string
	// Resource optionally specifies the resource name for this request.
	Resource string
	// QueryParams should be true in order to append the URL query values to the  "http.url" tag.
	QueryParams bool
	// Route is the request matched route if any, or is empty otherwise
	Route string
	// RouteParams specifies framework-specific route parameters (e.g. for route /user/:id coming
	// in as /user/123 we'll have {"id": "123"}). This field is optional and is used for monitoring
	// by AppSec. It is only taken into account when AppSec is enabled.
	RouteParams map[string]string
	// FinishOpts specifies any options to be used when finishing the request span.
	FinishOpts []ddtrace.FinishOption
	// SpanOpts specifies any options to be applied to the request starting span.
	SpanOpts []ddtrace.StartSpanOption
}

func BeforeHandle(cfg *ServeConfig, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func()) {
	if cfg == nil {
		cfg = new(ServeConfig)
	}
	opts := options.Copy(cfg.SpanOpts...) // make a copy of cfg.SpanOpts to avoid races
	if cfg.Service != "" {
		opts = append(opts, tracer.ServiceName(cfg.Service))
	}
	if cfg.Resource != "" {
		opts = append(opts, tracer.ResourceName(cfg.Resource))
	}
	if cfg.Route != "" {
		opts = append(opts, tracer.Tag(ext.HTTPRoute, cfg.Route))
	}
	span, ctx := httptrace.StartRequestSpan(r, opts...)
	rw, ddrw := wrapResponseWriter(w)
	afterHandle := func() {
		httptrace.FinishRequestSpan(span, ddrw.status, cfg.FinishOpts...)
	}

	//if appsec.Enabled() {
	//	h = httpsec.WrapHandler(h, span, cfg.RouteParams, nil)
	//}
	return rw, r.WithContext(ctx), afterHandle
}
