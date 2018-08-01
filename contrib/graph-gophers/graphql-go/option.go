package graphql

type config struct{ serviceName string }

// Option represents an option that can be used customize the Tracer.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "graphql.server"
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
