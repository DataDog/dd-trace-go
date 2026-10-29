// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package kafka

import (
	"context"
	"sync"

	"github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2/internal/tracing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/segmentio/kafka-go"
)

// ReaderTracer traces the messages that a kafka.Reader fetches. The otelc
// hooks of this package use it for readers the application did not wrap with
// [WrapReader]. It holds no reference to the reader.
type ReaderTracer struct {
	tracer *tracing.Tracer

	mu   sync.Mutex
	prev *tracer.Span
}

// NewReaderTracer returns a ReaderTracer configured from r.
func NewReaderTracer(r *kafka.Reader, opts ...Option) *ReaderTracer {
	return &ReaderTracer{tracer: tracing.NewTracer(readerKafkaConfig(r), opts...)}
}

// FinishPrevious finishes the span of the last message traced, if it is still
// open.
func (rt *ReaderTracer) FinishPrevious() {
	rt.mu.Lock()
	prev := rt.prev
	rt.prev = nil
	rt.mu.Unlock()
	if prev != nil {
		prev.Finish()
	}
}

// StartConsumeSpan starts the span for msg and injects its context into the
// message headers. The span stays open until the next call to FinishPrevious.
func (rt *ReaderTracer) StartConsumeSpan(ctx context.Context, msg *kafka.Message) {
	tMsg := wrapMessage(msg)
	span := rt.tracer.StartConsumeSpan(ctx, tMsg)
	rt.tracer.SetConsumeDSMCheckpoint(tMsg)
	rt.mu.Lock()
	prev := rt.prev
	rt.prev = span
	rt.mu.Unlock()
	if prev != nil {
		prev.Finish()
	}
}

// WriterTracer traces the messages that a kafka.Writer produces. The otelc
// hooks of this package use it for writers the application did not wrap with
// [WrapWriter]. It holds no reference to the writer.
type WriterTracer struct {
	tracer *tracing.Tracer
}

// NewWriterTracer returns a WriterTracer configured from w.
func NewWriterTracer(w *kafka.Writer, opts ...Option) *WriterTracer {
	return &WriterTracer{tracer: tracing.NewTracer(writerKafkaConfig(w), opts...)}
}

// StartProduceSpans starts one span for each message in msgs, which w is about
// to write, and injects its context into the message headers. Call the
// returned function with the result of the write to finish the spans.
//
// The spans are siblings, children of the span in ctx. They start from a nil
// context with an explicit parent because in an otelc build, starting each one
// from ctx would make it a child of the previous one through the goroutine
// local storage.
func (wt *WriterTracer) StartProduceSpans(ctx context.Context, w *kafka.Writer, msgs []kafka.Message) func(err error) {
	var opts []tracer.StartSpanOption
	if parent, ok := tracer.SpanFromContext(ctx); ok {
		opts = append(opts, tracer.ChildOf(parent.Context()))
	}
	tWriter := wrapTracingWriter(w)
	spans := make([]*tracer.Span, len(msgs))
	for i := range msgs {
		tMsg := wrapMessage(&msgs[i])
		//nolint:staticcheck // A nil context is wanted here, see above.
		spans[i] = wt.tracer.StartProduceSpan(nil, tWriter, tMsg, opts...)
		wt.tracer.SetProduceDSMCheckpoint(tMsg, tWriter)
	}
	return func(err error) {
		for i, span := range spans {
			wt.tracer.FinishProduceSpan(span, msgs[i].Partition, msgs[i].Offset, err)
		}
	}
}
