package llmobs

import (
	"context"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

type Tracer interface {
	StartSpan(ctx context.Context, name string, cfg StartAPMSpanConfig) (APMSpan, context.Context)
}

type StartAPMSpanConfig struct {
	SpanType  string
	StartTime time.Time
}

type FinishAPMSpanConfig struct {
	FinishTime time.Time
	Error      error
}

type APMSpan interface {
	Finish(cfg FinishAPMSpanConfig)
	AddLink(link SpanLink)
	SpanID() string
	TraceID() string
	SetBaggageItem(key string, value string)
}

type SpanLink = transport.SpanLink
