package vault

import "math"

type config struct {
	withAnalytics bool
	analyticsRate float64
	serviceName   string
}

// Option can be passed to NewHTTPClient and WrapHTTPClient to configure the integration.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "vault"
	cfg.analyticsRate = math.NaN()
}

// WithAnalytics enables or disables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(c *config) {
		c.withAnalytics = on
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(c *config) {
		c.analyticsRate = rate
	}
}

// WithServiceName sets the given service name for the http.Client.
func WithServiceName(name string) Option {
	return func(c *config) {
		c.serviceName = name
	}
}
