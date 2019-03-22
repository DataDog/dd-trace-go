package mongo

type config struct {
	serviceName   string
	analyticsRate float64
}

// Option represents an option that can be passed to Dial.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "mongo"
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the dialled connection.
// When the service name is not explicitly set it will be inferred based on the
// request to AWS.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
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
