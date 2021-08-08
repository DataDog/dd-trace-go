package negroni

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/urfave/negroni"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// DatadogMiddleware returns middleware that will trace incoming requests.
type DatadogMiddleware struct {
	cfg *config
}

func (m *DatadogMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.ServiceName(m.cfg.serviceName),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, r.URL.Path),
		tracer.Tag(ext.ResourceName, fmt.Sprintf("%s %s", r.Method, r.URL.Path)),
		tracer.Measured(),
	}
	if !math.IsNaN(m.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, m.cfg.analyticsRate))
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	opts = append(opts, m.cfg.spanOpts...)
	span, ctx := tracer.StartSpanFromContext(r.Context(), "http.request", opts...)
	defer span.Finish()

	r = r.WithContext(ctx)

	next(w, r)

	responseWriter := w.(negroni.ResponseWriter)
	status := responseWriter.Status()
	span.SetTag(ext.HTTPCode, strconv.Itoa(status))
	if status >= 500 && status < 600 {
		span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
	}
}

// Middleware create the negroni middleware that will trace incoming requests
func Middleware(opts ...Option) *DatadogMiddleware {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/urgave/negroni: Configuring Middleware: %#v", cfg)

	m := DatadogMiddleware{
		cfg: cfg,
	}

	return &m
}
