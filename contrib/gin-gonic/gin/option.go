package gin

import "gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

type config struct {
	analyticsRate float64
}

func newConfig() *config {
	return &config{
		analyticsRate: globalconfig.AnalyticsRate(),
	}
}

// Option specifies instrumentation configuration options.
type Option func(*config)

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
