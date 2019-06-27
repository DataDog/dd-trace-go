package sql

import (
	"math"
)

type registerConfig struct {
	serviceName   string
	analyticsRate float64
}

// RegisterOption represents an option that can be passed to Register.
type RegisterOption func(*registerConfig)

func defaults(cfg *registerConfig) {
	// default cfg.serviceName set in Register based on driver name
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	cfg.analyticsRate = math.NaN()
}

// WithServiceName sets the given service name for the registered driver.
func WithServiceName(name string) RegisterOption {
	return func(cfg *registerConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) RegisterOption {
	return func(cfg *registerConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) RegisterOption {
	return func(cfg *registerConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

type openConfig struct {
	serviceName   string
	analyticsRate float64
	dsn           string
}

// OpenOption represents an option that can be passed to Open or OpenDB.
type OpenOption func(*openConfig)

func openDefaults(cfg *openConfig) {
	cfg.analyticsRate = math.NaN()
}

type openOptions struct{}

// OpenOptions is used to create the OpenOption values used in Open or OpenDB.
var OpenOptions openOptions

func (openOptions) defaults(cfg *openConfig) {
	// default cfg.serviceName set in Register based on driver name
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	cfg.analyticsRate = math.NaN()
}

// WithServiceName sets the given service name for the opened driver.
func (openOptions) WithServiceName(name string) OpenOption {
	return func(cfg *openConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func (openOptions) WithAnalytics(on bool) OpenOption {
	return func(cfg *openConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func (openOptions) WithAnalyticsRate(rate float64) OpenOption {
	return func(cfg *openConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithDataSourceName allows the data source name to be provided when
// using OpenDB and a driver.Connector.
// The value is used to automatically set tags on spans.
func (openOptions) WithDataSourceName(name string) OpenOption {
	return func(cfg *openConfig) {
		cfg.dsn = name
	}
}
