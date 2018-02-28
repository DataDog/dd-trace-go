package elastic

import "net/http"

type clientConfig struct {
	serviceName string
	transport   *http.Transport
}

// ClientOption represents an option that can be used when creating a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.serviceName = "elastic.client"
	cfg.transport = http.DefaultTransport.(*http.Transport)
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithTransport sets the given transport as an http.Transport for the client.
func WithTransport(t *http.Transport) ClientOption {
	return func(cfg *clientConfig) {
		cfg.transport = t
	}
}
