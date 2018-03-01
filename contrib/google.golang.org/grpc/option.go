package grpc

type interceptorConfig struct{ serviceName string }

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
type InterceptorOption func(*interceptorConfig)

func defaults(cfg *interceptorConfig) {
	// cfg.serviceName defaults are set in interceptors
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.serviceName = name
	}
}
