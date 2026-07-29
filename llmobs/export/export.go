// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"
	"fmt"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/exportutil"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// SubmitSpans exports completed LLM Obs spans.
func (c *Client) SubmitSpans(ctx context.Context, events []SpanEvent, opts ...SubmitSpansOption) (*ExportResult, error) {
	sc := c.resolveSubmitSpans(opts)
	res := &ExportResult{}
	size := c.spanBatch
	if size <= 0 {
		size = len(events)
	}
	for start := 0; start < len(events); start += size {
		if err := ctx.Err(); err != nil {
			return res, res.recordCancel(len(events)-start, err)
		}
		end := min(start+size, len(events))
		batch := make([]spanRow, 0, end-start)
		for i := start; i < end; i++ {
			e := events[i]
			if reason := illmobs.ValidateExportSpan(e); reason != nil {
				reason.Index = i
				res.ValidationErrors = append(res.ValidationErrors, *reason)
				continue
			}
			ws := illmobs.BuildExportSpan(
				e,
				sc.service,
				c.config.TracerConfig.Env,
				c.config.TracerConfig.Version,
				c.config.MLApp,
			)
			batch = append(batch, spanRow{index: i, span: ws})
		}
		c.sendSpanBatch(ctx, batch, res)
	}
	failed := res.finalize()
	// Only report cancellation when rows were abandoned.
	if err := res.canceledErr(); err != nil {
		return res, err
	}
	return res, exportutil.Aggregate(failed, len(res.Requests), "llmobs/export")
}

type spanRow struct {
	index int
	span  *transport.LLMObsSpanEvent
}

// SubmitEvaluations exports LLM Obs evaluation metrics.
func (c *Client) SubmitEvaluations(ctx context.Context, evals []EvaluationMetric, opts ...SubmitEvaluationsOption) (*ExportResult, error) {
	sc := c.resolveSubmitEvaluations(opts)
	res := &ExportResult{}
	size := c.evalBatch
	if size <= 0 {
		size = len(evals)
	}
	for start := 0; start < len(evals); start += size {
		if err := ctx.Err(); err != nil {
			return res, res.recordCancel(len(evals)-start, err)
		}
		end := min(start+size, len(evals))
		batch := make([]evalRow, 0, end-start)
		for i := start; i < end; i++ {
			w, reason := illmobs.BuildExportEvaluation(evals[i], sc.mlApp)
			if reason != nil {
				reason.Index = i
				res.ValidationErrors = append(res.ValidationErrors, *reason)
				continue
			}
			batch = append(batch, evalRow{index: i, metric: w})
		}
		c.sendEvalBatch(ctx, batch, res)
	}
	failed := res.finalize()
	if err := res.canceledErr(); err != nil {
		return res, err
	}
	return res, exportutil.Aggregate(failed, len(res.Requests), "llmobs/export")
}

type evalRow struct {
	index  int
	metric *transport.LLMObsMetric
}

func (c *Client) sendEvalBatch(ctx context.Context, batch []evalRow, res *ExportResult) {
	if len(batch) == 0 {
		return
	}
	metrics := make([]*transport.LLMObsMetric, len(batch))
	for i := range batch {
		metrics[i] = batch[i].metric
	}
	payload := transport.PushMetricsRequest{
		Data: transport.PushMetricsRequestData{
			Type:       "evaluation_metric",
			Attributes: transport.PushMetricsRequestDataAttributes{Metrics: metrics},
		},
	}
	_, err := transport.MarshalJSON(payload)
	if err != nil {
		good := dropUnencodableEvals(batch, res)
		if len(good) == len(batch) {
			res.Requests = append(res.Requests, RequestResult{Index: len(res.Requests), Count: len(batch), Err: fmt.Errorf("llmobs/export: encode eval payload: %w", err)})
			return
		}
		if len(good) > 0 {
			c.sendEvalBatch(ctx, good, res)
		}
		return
	}
	rr := RequestResult{Index: len(res.Requests), Count: len(batch)}
	r, perr := c.transport.PushEvalMetricsWithResult(ctx, metrics)
	applyResult(&rr, r, perr)
	res.Requests = append(res.Requests, rr)
}

func dropUnencodableEvals(batch []evalRow, res *ExportResult) []evalRow {
	good := make([]evalRow, 0, len(batch))
	for _, r := range batch {
		if _, err := transport.MarshalJSON(r.metric); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  r.index,
				Code:   CodeNotEncodable,
				Reason: "evaluation is not JSON-encodable: " + err.Error(),
			})
			continue
		}
		good = append(good, r)
	}
	return good
}

func (c *Client) sendSpanBatch(ctx context.Context, batch []spanRow, res *ExportResult) {
	if len(batch) == 0 {
		return
	}
	body, err := marshalSpanPayload(batch)
	if err != nil {
		good := dropUnencodableSpans(batch, res)
		if len(good) == len(batch) {
			res.Requests = append(res.Requests, RequestResult{Index: len(res.Requests), Count: len(batch), Err: fmt.Errorf("llmobs/export: encode span payload: %w", err)})
			return
		}
		if len(good) > 0 {
			c.sendSpanBatch(ctx, good, res)
		}
		return
	}
	if len(body) > c.maxSpanBytes && len(batch) > 1 {
		mid := len(batch) / 2
		c.sendSpanBatch(ctx, batch[:mid], res)
		if err := ctx.Err(); err != nil {
			_ = res.recordCancel(len(batch[mid:]), err)
			return
		}
		c.sendSpanBatch(ctx, batch[mid:], res)
		return
	}
	rr := RequestResult{Index: len(res.Requests), Count: len(batch)}
	if len(body) > c.maxSpanBytes {
		if body, err = dropSpanIO(batch); err != nil {
			rr.Err = fmt.Errorf("llmobs/export: encode span payload: %w", err)
			res.Requests = append(res.Requests, rr)
			return
		}
	}
	r, perr := c.transport.PushSpanEventsWithResult(ctx, spanEvents(batch))
	applyResult(&rr, r, perr)
	res.Requests = append(res.Requests, rr)
}

func spanEvents(batch []spanRow) []*transport.LLMObsSpanEvent {
	spans := make([]*transport.LLMObsSpanEvent, len(batch))
	for i := range batch {
		spans[i] = batch[i].span
	}
	return spans
}

func marshalSpanPayload(batch []spanRow) ([]byte, error) {
	return transport.MarshalJSON(transport.NewPushSpanEventsRequests(spanEvents(batch)))
}

func dropUnencodableSpans(batch []spanRow, res *ExportResult) []spanRow {
	good := make([]spanRow, 0, len(batch))
	for _, r := range batch {
		if _, err := transport.MarshalJSON(r.span); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  r.index,
				Code:   CodeNotEncodable,
				Reason: "span is not JSON-encodable: " + err.Error(),
			})
			continue
		}
		good = append(good, r)
	}
	return good
}

func dropSpanIO(batch []spanRow) ([]byte, error) {
	for _, r := range batch {
		illmobs.DropSpanEventIO(r.span)
	}
	return marshalSpanPayload(batch)
}

func applyResult(rr *RequestResult, r transport.RequestResult, err error) {
	rr.StatusCode = r.StatusCode
	rr.Attempts = r.Attempts
	rr.Retriable = r.Retriable
	rr.ResponseSnippet = exportutil.Snippet(r.Body)
	rr.Err = err
}
