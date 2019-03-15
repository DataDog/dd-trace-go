package memcache

import "gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

const (
	serviceName   = "memcached"
	operationName = "memcached.query"
)

type clientConfig struct {
	serviceName   string
	analyticsRate float64
}

// ClientOption represents an option that can be passed to Dial.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.serviceName = serviceName
	cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOption {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(0.0)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOption {
	return func(cfg *clientConfig) {
		cfg.analyticsRate = rate
	}
}
