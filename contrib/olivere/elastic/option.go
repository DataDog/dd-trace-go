package elastic

import "net/http"

type clientConfig struct {
	serviceName   string
	transport     *http.Transport
	analyticsRate float64
}

// ClientOption represents an option that can be used when creating a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.serviceName = "elastic.client"
	cfg.transport = http.DefaultTransport.(*http.Transport)
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithTransport sets the given transport as an http.Transport for the client.
func WithTransport(t *http.Transport) ClientOption {
	return func(cfg *clientConfig) {
		cfg.transport = t
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
