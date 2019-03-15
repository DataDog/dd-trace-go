package redigo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/garyburd/redigo"

import "gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

type dialConfig struct {
	serviceName   string
	analyticsRate float64
}

// DialOption represents an option that can be passed to Dial.
type DialOption func(*dialConfig)

func defaults(cfg *dialConfig) {
	cfg.serviceName = "redis.conn"
	cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) DialOption {
	return func(cfg *dialConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) DialOption {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(0.0)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) DialOption {
	return func(cfg *dialConfig) {
		cfg.analyticsRate = rate
	}
}
