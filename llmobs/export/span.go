// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"
	"fmt"
	"maps"
	"time"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// Kind is the LLM Obs span kind.
type Kind = transport.SpanKind

const (
	KindLLM       Kind = transport.SpanKindLLM
	KindAgent     Kind = transport.SpanKindAgent
	KindWorkflow  Kind = transport.SpanKindWorkflow
	KindTask      Kind = transport.SpanKindTask
	KindTool      Kind = transport.SpanKindTool
	KindEmbedding Kind = transport.SpanKindEmbedding
	KindRetrieval Kind = transport.SpanKindRetrieval
)

// Status is the terminal status of a span.
type Status = transport.SpanStatus

const (
	StatusOK    Status = transport.SpanStatusOK
	StatusError Status = transport.SpanStatusError
)

// SpanEvent is a completed LLM Obs span.
type SpanEvent = transport.LLMObsSpanEvent

// SpanLink links an LLM Obs span.
type SpanLink = transport.SpanLink

// DDAttributes contains Datadog correlation attributes for a span.
type DDAttributes = transport.DDAttributes

// ErrorMessage contains error details for a span.
type ErrorMessage = transport.ErrorMessage

// SpanEventOption configures a span built by [NewSpanEvent].
type SpanEventOption func(*SpanEvent)

// NewSpanEvent constructs a completed span.
func NewSpanEvent(traceID, spanID string, kind Kind, opts ...SpanEventOption) SpanEvent {
	event := SpanEvent{
		SpanID:   spanID,
		TraceID:  traceID,
		ParentID: illmobs.DefaultParentID,
		Name:     string(kind),
		Status:   StatusOK,
		Meta:     illmobs.NewSpanEventMeta(kind),
		DDAttributes: DDAttributes{
			SpanID:  spanID,
			TraceID: traceID,
		},
	}
	for _, opt := range opts {
		opt(&event)
	}
	return event
}

// WithTiming sets the span start time and duration.
func WithTiming(start time.Time, duration time.Duration) SpanEventOption {
	return func(event *SpanEvent) {
		if start.IsZero() {
			event.StartNS = 0
		} else {
			event.StartNS = start.UnixNano()
		}
		event.Duration = duration
	}
}

// WithModel sets model details for an LLM or embedding span.
func WithModel(name, provider string) SpanEventOption {
	return func(event *SpanEvent) {
		illmobs.SetSpanModelMeta(spanEventMeta(event), spanEventKind(event), name, provider)
	}
}

// WithTextIO sets text input and output.
func WithTextIO(input, output string) SpanEventOption {
	return func(event *SpanEvent) {
		meta := spanEventMeta(event)
		if input == "" {
			delete(meta, "input")
		} else {
			meta["input"] = map[string]any{"value": input}
		}
		if output == "" {
			delete(meta, "output")
		} else {
			meta["output"] = map[string]any{"value": output}
		}
	}
}

// WithMetadata sets span metadata.
func WithMetadata(metadata map[string]any) SpanEventOption {
	return func(event *SpanEvent) {
		meta := spanEventMeta(event)
		if len(metadata) == 0 {
			delete(meta, "metadata")
			return
		}
		meta["metadata"] = maps.Clone(metadata)
	}
}

// WithSpanError marks the span as failed and sets its error details.
func WithSpanError(details ErrorMessage) SpanEventOption {
	return func(event *SpanEvent) {
		event.Status = StatusError
		illmobs.SetSpanErrorMeta(spanEventMeta(event), &details)
	}
}

func spanEventMeta(event *SpanEvent) map[string]any {
	if event.Meta == nil {
		event.Meta = make(map[string]any)
	}
	return event.Meta
}

func spanEventKind(event *SpanEvent) Kind {
	kind, _ := event.Meta["span.kind"].(string)
	return Kind(kind)
}

// SubmitSpans submits completed LLM Obs spans.
func (c *Client) SubmitSpans(ctx context.Context, events []SpanEvent, opts ...SubmitSpansOption) (*Result, error) {
	sc := c.resolveSubmitSpans(opts)
	res := &Result{}
	size := defaultSpanBatchSize
	for start := 0; start < len(events); start += size {
		if err := ctx.Err(); err != nil {
			return res, res.recordCancel(inputIndices(start, len(events)), err)
		}
		end := min(start+size, len(events))
		batch := make([]spanRow, 0, end-start)
		for i := start; i < end; i++ {
			event := events[i]
			if validation := illmobs.ValidateExportSpan(event); validation != nil {
				validation.Index = i
				res.ValidationErrors = append(res.ValidationErrors, *validation)
				continue
			}
			batch = append(batch, spanRow{
				index: i,
				span:  illmobs.BuildExportSpan(event, c.config, sc.service),
			})
		}
		c.sendSpanBatch(ctx, batch, res)
	}
	res.finalize()
	if err := res.canceledErr(); err != nil {
		return res, err
	}
	return res, aggregateFailures(res)
}

type spanRow struct {
	index int
	span  *transport.LLMObsSpanEvent
}

func (c *Client) sendSpanBatch(ctx context.Context, batch []spanRow, res *Result) {
	if len(batch) == 0 {
		return
	}
	body, err := marshalSpanPayload(batch)
	if err != nil {
		good := dropUnencodableSpans(batch, res)
		if len(good) == len(batch) {
			res.Requests = append(res.Requests, RequestResult{
				InputIndices: spanInputIndices(batch),
				Err:          fmt.Errorf("llmobs/export: encode span payload: %w", err),
			})
			return
		}
		if len(good) > 0 {
			c.sendSpanBatch(ctx, good, res)
		}
		return
	}
	if len(body) > illmobs.SizeLimitEVPEvent && len(batch) > 1 {
		mid := len(batch) / 2
		c.sendSpanBatch(ctx, batch[:mid], res)
		if err := ctx.Err(); err != nil {
			_ = res.recordCancel(spanInputIndices(batch[mid:]), err)
			return
		}
		c.sendSpanBatch(ctx, batch[mid:], res)
		return
	}
	rr := RequestResult{InputIndices: spanInputIndices(batch)}
	if len(body) > illmobs.SizeLimitEVPEvent {
		if body, err = dropSpanIO(batch); err != nil {
			rr.Err = fmt.Errorf("llmobs/export: encode span payload: %w", err)
			res.Requests = append(res.Requests, rr)
			return
		}
		if len(body) > illmobs.SizeLimitEVPEvent {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  batch[0].index,
				Code:   CodeTooLarge,
				Reason: "span exceeds the LLM Obs event size limit after dropping input and output",
			})
			return
		}
	}
	result, requestErr := c.transport.PushSpanEventsWithResult(ctx, spanEvents(batch))
	applyResult(&rr, result, requestErr)
	res.Requests = append(res.Requests, rr)
}

func spanEvents(batch []spanRow) []*transport.LLMObsSpanEvent {
	spans := make([]*transport.LLMObsSpanEvent, len(batch))
	for i := range batch {
		spans[i] = batch[i].span
	}
	return spans
}

func spanInputIndices(batch []spanRow) []int {
	indices := make([]int, len(batch))
	for i, row := range batch {
		indices[i] = row.index
	}
	return indices
}

func marshalSpanPayload(batch []spanRow) ([]byte, error) {
	return transport.MarshalJSON(transport.NewPushSpanEventsRequests(spanEvents(batch)))
}

func dropUnencodableSpans(batch []spanRow, res *Result) []spanRow {
	good := make([]spanRow, 0, len(batch))
	for _, row := range batch {
		if _, err := transport.MarshalJSON(row.span); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  row.index,
				Code:   CodeNotEncodable,
				Reason: "span is not JSON-encodable: " + err.Error(),
			})
			continue
		}
		good = append(good, row)
	}
	return good
}

func dropSpanIO(batch []spanRow) ([]byte, error) {
	for _, row := range batch {
		illmobs.DropSpanEventIO(row.span)
	}
	return marshalSpanPayload(batch)
}
