package elastic

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/tracer"
)

type clientConfig struct {
	serviceName string
	transport   *http.Transport
	tracer      *tracer.Tracer // TODO(gbbr): Remove this when we switch.
}

// ClientOption represents an option that can be used when creating a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.tracer = tracer.DefaultTracer
	cfg.serviceName = "elastic.client"
	cfg.transport = http.DefaultTransport.(*http.Transport)
}

// WithServiceName sets the given service name for the registered driver.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

func WithTransport(t *http.Transport) ClientOption {
	return func(cfg *clientConfig) {
		cfg.transport = t
	}
}

func WithTracer(t *tracer.Tracer) ClientOption {
	return func(cfg *clientConfig) {
		cfg.tracer = t
	}
}
