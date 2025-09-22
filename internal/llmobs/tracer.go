// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

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
