package tracing

import (
	"net/http"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "julienschmidt/httprouter"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/julienschmidt/httprouter")
}

type Router interface {
	Lookup(method string, path string) (any, []Param, bool)
}

type Param interface {
	GetKey() string
	GetValue() string
}

func BeforeHandle[T any, WT Router](cfg *Config, router T, wrapRouter func(T) WT, w http.ResponseWriter, req *http.Request) (http.ResponseWriter, *http.Request, func(), bool) {
	wRouter := wrapRouter(router)
	// get the resource associated to this request
	route := req.URL.Path
	_, ps, _ := wRouter.Lookup(req.Method, route)
	for _, param := range ps {
		route = strings.Replace(route, param.GetValue(), ":"+param.GetKey(), 1)
	}

	resource := req.Method + " " + route
	spanOpts := options.Copy(cfg.spanOpts...) // spanOpts must be a copy of r.config.spanOpts, locally scoped, to avoid races.
	spanOpts = append(spanOpts, httptrace.HeaderTagsFromRequest(req, cfg.headerTags))

	serveCfg := &httptrace.ServeConfig{
		Service:  cfg.serviceName,
		Resource: resource,
		SpanOpts: spanOpts,
		Route:    route,
	}
	return httptrace.BeforeHandle(serveCfg, w, req)
}
