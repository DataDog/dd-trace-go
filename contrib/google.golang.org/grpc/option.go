package grpc

// Option specifies a configuration option for the grpc package. Not all options apply
// to all instrumented structures.
type Option = InterceptorOption

type config struct {
	serviceName                           string
	traceStreamCalls, traceStreamMessages bool
	noDebugStack                          bool
	analyticsRate                         float64
}

func (cfg *config) serverServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.server"
	}
	return cfg.serviceName
}

func (cfg *config) clientServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.client"
	}
	return cfg.serviceName
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
// InterceptorOption is deprecated in favor of Option.
type InterceptorOption func(*config)

func defaults(cfg *config) {
	// cfg.serviceName defaults are set in interceptors
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithStreamCalls enables or disables tracing of streaming calls. This option does not apply to the
// stats handler.
func WithStreamCalls(enabled bool) Option {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables tracing of streaming messages. This option does not apply
// to the stats handler.
func WithStreamMessages(enabled bool) Option {
	return func(cfg *config) {
		cfg.traceStreamMessages = enabled
	}
}

// NoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent and the overhead of calling debug.Stack may affect performance.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(0.0)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		cfg.analyticsRate = rate
	}
}
