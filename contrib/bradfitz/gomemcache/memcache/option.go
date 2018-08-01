package memcache

const (
	serviceName   = "memcached"
	operationName = "memcached.query"
)

type clientConfig struct{ serviceName string }

// ClientOption represents an option that can be passed to Dial.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.serviceName = serviceName
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}
