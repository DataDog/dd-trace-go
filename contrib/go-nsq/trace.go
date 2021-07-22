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

func (this *traceHelper) SetMetaTag(key string, value interface{}) {
	this.meta[key] = value
}

func (this *traceHelper) trace(start time.Time, spType spanType, opType string, err error) {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(this.cfg.service),
		tracer.SpanType(string(spType)),
		tracer.StartTime(start),
	}

	if !math.IsNaN(this.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, this.cfg.analyticsRate))
	}

	span, ctx := tracer.StartSpanFromContext(this.cfg.ctx, opType, opts...)
	this.cfg.ctx = ctx

	for k, v := range this.meta {
		span.SetTag(k, v)
	}

	span.Finish(tracer.WithError(err))
}
