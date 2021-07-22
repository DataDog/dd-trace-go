package nsq

import (
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type traceHelper struct {
	cfg  *Config
	meta map[string]interface{}
}

func newTraceHelper(conf *Config) *traceHelper {
	return &traceHelper{
		cfg:  conf,
		meta: make(map[string]interface{}),
	}
}

func (th *traceHelper) SetMetaTag(key string, value interface{}) {
	th.meta[key] = value
}

func (th *traceHelper) trace(start time.Time, spType string, opType string, err error) {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(th.cfg.service),
		tracer.SpanType(spType),
		tracer.StartTime(start),
	}
	if !math.IsNaN(th.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, th.cfg.analyticsRate))
	}

	span, ctx := tracer.StartSpanFromContext(th.cfg.ctx, opType, opts...)
	th.cfg.ctx = ctx

	for k, v := range th.meta {
		span.SetTag(k, v)
	}

	span.Finish(tracer.WithError(err))
}
