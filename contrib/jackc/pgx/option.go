package pgx

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

// Option is a function that modifies the configuration.
type Option func(*tracer)

func defaults() *tracer {
	analyticsRate := math.NaN()
	if internal.BoolEnv("DD_TRACE_PGX_ENABLED", false) {
		analyticsRate = 1.0
	}
	return &tracer{
		serviceName:   "postgres.db",
		analyticsRate: analyticsRate,
	}
}

// WithServiceName sets the service name.
func WithServiceName(name string) Option {
	return func(t *tracer) {
		t.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(t *tracer) {
		if on {
			t.analyticsRate = 1.0
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(t *tracer) {
		if rate >= 0.0 && rate <= 1.0 {
			t.analyticsRate = rate
		}
	}
}

// WithCustomTag will attach the value to the span tagged by the key
func WithCustomTag(key string, value interface{}) Option {
	return func(t *tracer) {
		if t.tags == nil {
			t.tags = make(map[string]interface{})
		}
		t.tags[key] = value
	}
}

// TraceArgs will report the arguments of the queries, if on is set to true
func TraceArgs(on bool) Option {
	return func(t *tracer) {
		t.traceArgs = on
	}
}

// TraceStatus will report the status of the queries, if on is set to true
func TraceStatus(on bool) Option {
	return func(t *tracer) {
		t.traceStatus = on
	}
}
