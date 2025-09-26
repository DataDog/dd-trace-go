package tracer

import (
	"context"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

type llmobsTracerAdapter struct{}

func (l *llmobsTracerAdapter) StartSpan(ctx context.Context, name string, cfg llmobs.StartAPMSpanConfig) (llmobs.APMSpan, context.Context) {
	opts := make([]StartSpanOption, 0)
	if !cfg.StartTime.IsZero() {
		opts = append(opts, StartTime(cfg.StartTime))
	}
	if cfg.SpanType != "" {
		opts = append(opts, SpanType(cfg.SpanType))
	}
	span, ctx := StartSpanFromContext(ctx, name, opts...)
	return &llmobsSpanAdapter{span}, ctx
}

type llmobsSpanAdapter struct {
	span *Span
}

func (l *llmobsSpanAdapter) Finish(cfg llmobs.FinishAPMSpanConfig) {
	opts := make([]FinishOption, 0)
	if !cfg.FinishTime.IsZero() {
		opts = append(opts, FinishTime(cfg.FinishTime))
	}
	if cfg.Error != nil {
		opts = append(opts, WithError(cfg.Error))
	}
	l.span.Finish(opts...)
}

func (l *llmobsSpanAdapter) AddLink(link llmobs.SpanLink) {
	l.span.AddLink(SpanLink{
		TraceID:     link.TraceID,
		TraceIDHigh: link.TraceIDHigh,
		SpanID:      link.SpanID,
		Attributes:  link.Attributes,
		Tracestate:  link.Tracestate,
		Flags:       link.Flags,
	})
}

func (l *llmobsSpanAdapter) SpanID() string {
	return strconv.FormatUint(l.span.Context().SpanID(), 10)
}

func (l *llmobsSpanAdapter) TraceID() string {
	return l.span.Context().TraceID()
}

func (l *llmobsSpanAdapter) SetBaggageItem(key string, value string) {
	l.span.SetBaggageItem(key, value)
}
