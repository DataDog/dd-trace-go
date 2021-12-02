package httprouter // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/bouk/httprouter"

import (
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httputil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"math"
	"net/http"
	"strings"

	"github.com/bouk/httprouter"
)

// Router is a traced version of httprouter.Router.
type Router struct {
	*httprouter.Router
	config *routerConfig
}

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
	cfg := new(routerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	cfg.spanOpts = append(cfg.spanOpts, tracer.Measured())
	log.Debug("contrib/bouk/httprouter: Configuring Router: %#v", cfg)
	return &Router{httprouter.New(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {

	// get the resource associated to this request
	route := req.URL.Path
	_, ps, _ := r.Router.Lookup(req.Method, route)
	for _, param := range ps {
		route = strings.Replace(route, param.Value, ":"+param.Key, 1)
	}
	resource := req.Method + " " + route
	httputil.TraceAndServe(r.Router, &httputil.TraceConfig{
		ResponseWriter: w,
		Request:        req,
		Service:        r.config.serviceName,
		Resource:       resource,
		SpanOpts:       r.config.spanOpts,
	})
}
