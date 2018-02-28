package gocql

type queryConfig struct{ serviceName string }

// WrapOption represents an option that can be passed to WrapQuery.
type WrapOption func(*queryConfig)

func defaults(cfg *queryConfig) {
	cfg.serviceName = "gocql.query"
}

// WithServiceName sets the given service name for the returned query.
func WithServiceName(name string) WrapOption {
	return func(cfg *queryConfig) {
		cfg.serviceName = name
	}
}
