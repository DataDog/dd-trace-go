package httprouter

import "github.com/DataDog/dd-trace-go/tracer"

type routerConfig struct {
	serviceName string
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// RouterOption represents an option that can be passed to New.
type RouterOption func(*routerConfig)

func defaults(cfg *routerConfig) {
	cfg.serviceName = "http.router"
	cfg.tracer = tracer.DefaultTracer
}

// WithServiceName sets the given service name for the returned router.
func WithServiceName(name string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

func WithTracer(t *tracer.Tracer) RouterOption {
	return func(cfg *routerConfig) {
		cfg.tracer = t
	}
}
