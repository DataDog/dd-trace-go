package gocql

type queryConfig struct{ serviceName, resourceName string }

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

// WithResourceName sets a custom resource name to be used with the traced query.
// By default, the query statement is extracted automatically. This method should
// be used when a different resource name is desired or in performance critical
// environments. The gocql library returns the query statement using an fmt.Sprintf
// call, which can be costly when called repeatedly. Using WithResourceName will
// avoid that call. Under normal circumstances, it is safe to rely on the default.
func WithResourceName(name string) WrapOption {
	return func(cfg *queryConfig) {
		cfg.resourceName = name
	}
}
