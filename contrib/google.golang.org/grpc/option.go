package grpc

type interceptorConfig struct {
	serviceName                           string
	traceStreamCalls, traceStreamMessages bool
	noDebugStack                          bool
}

func (cfg *interceptorConfig) serverServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.server"
	}
	return cfg.serviceName
}

func (cfg *interceptorConfig) clientServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.client"
	}
	return cfg.serviceName
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
type InterceptorOption func(*interceptorConfig)

func defaults(cfg *interceptorConfig) {
	// cfg.serviceName defaults are set in interceptors
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.serviceName = name
	}
}

// WithStreamCalls enables or disables tracing of streaming calls.
func WithStreamCalls(enabled bool) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables tracing of streaming messages.
func WithStreamMessages(enabled bool) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.traceStreamMessages = enabled
	}
}

// NoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent and the overhead of calling debug.Stack may affect performance.
func NoDebugStack() InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.noDebugStack = true
	}
}

type statsHandlerConfig struct {
	serviceName  string
	noDebugStack bool
}

func newStatsHandlerConfig() *statsHandlerConfig {
	return &statsHandlerConfig{}
}

func (cfg *statsHandlerConfig) serverServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.server"
	}
	return cfg.serviceName
}

func (cfg *statsHandlerConfig) clientServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.client"
	}
	return cfg.serviceName
}

// StatsHandlerOption represents an option that can be passed
// to the grpc client and server stats handlers.
type StatsHandlerOption func(*statsHandlerConfig)

// StatsOptServiceName sets the given service name.
func StatsOptServiceName(name string) StatsHandlerOption {
	return func(cfg *statsHandlerConfig) {
		cfg.serviceName = name
	}
}

// StatsOptNoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent and the overhead of calling debug.Stack may affect performance.
func StatsOptNoDebugStack() StatsHandlerOption {
	return func(cfg *statsHandlerConfig) {
		cfg.noDebugStack = true
	}
}
