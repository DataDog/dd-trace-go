package aws

type config struct {
	serviceName string
}

// Option represents an option that can be passed to Dial.
type Option func(*config)

// WithServiceName sets the given service name for the dialled connection.
// When the service name is not explicitly set it will be inferred based on the
// request to AWS.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}
