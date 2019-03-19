package sql

type registerConfig struct {
	serviceName   string
	analyticsRate float64
}

// RegisterOption represents an option that can be passed to Register.
type RegisterOption func(*registerConfig)

func defaults(cfg *registerConfig) {
	// default cfg.serviceName set in Register based on driver name
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the registered driver.
func WithServiceName(name string) RegisterOption {
	return func(cfg *registerConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) RegisterOption {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(0.0)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) RegisterOption {
	return func(cfg *registerConfig) {
		cfg.analyticsRate = rate
	}
}
