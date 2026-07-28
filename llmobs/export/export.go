// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"
	"fmt"
	"strings"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/exportutil"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// SubmitSpans exports LLM Obs spans. Rows that fail validation are dropped and
// reported in ExportResult.ValidationErrors. The input is scanned in windows of
// the client's span batch size and each window is POSTed once, so peak memory is
// bounded by one batch rather than the whole input. The returned error is
// non-nil if any request failed; per-request detail is in the result.
func (c *Client) SubmitSpans(ctx context.Context, events []SpanEvent, opts ...SubmitSpansOption) (*ExportResult, error) {
	sc := c.resolveSubmitSpans(opts)
	res := &ExportResult{}
	size := c.spanBatch
	if size <= 0 {
		size = len(events)
	}
	for start := 0; start < len(events); start += size {
		if err := ctx.Err(); err != nil {
			return res, cancel(res, len(events)-start, err)
		}
		end := min(start+size, len(events))
		batch := make([]spanRow, 0, end-start)
		for i := start; i < end; i++ {
			e := events[i]
			if reason := validateSpan(e); reason != nil {
				res.ValidationErrors = append(res.ValidationErrors, ValidationError{Index: i, Code: reason.Code, Reason: reason.Reason})
				continue
			}
			ws := e.toWire(sc.service)
			ws.Tags = stampTags(ws.Tags, c.env, c.version, c.mlApp)
			// The intake reads service from the tag, not the top-level field, and
			// SessionID is the source of truth for the tag evaluations tag-join on. Both
			// replace rather than default, so a stale caller tag cannot disagree with
			// the structured field.
			ws.Tags = replaceTag(ws.Tags, "service", ws.Service)
			ws.Tags = replaceTag(ws.Tags, "session_id", e.SessionID)
			batch = append(batch, spanRow{index: i, span: ws})
		}
		c.sendSpanBatch(ctx, batch, res)
	}
	failed := res.finalize()
	// A cancellation inside the final window's bisection stops sendSpanBatch
	// between halves, after the loop guard above has run for the last time.
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("llmobs/export: export canceled: %w", err)
	}
	return res, exportutil.Aggregate(failed, len(res.Requests), "llmobs/export")
}

// cancel records the rows a canceled call never validated or sent as one not-sent
// request, so Sent+Dropped+Failed still covers the whole input (see ExportResult),
// and returns the error the call reports.
func cancel(res *ExportResult, remaining int, err error) error {
	if remaining > 0 {
		res.Requests = append(res.Requests, RequestResult{
			Index: len(res.Requests),
			Count: remaining,
			Err:   fmt.Errorf("llmobs/export: batch not sent, export canceled: %w", err),
		})
	}
	res.finalize()
	return fmt.Errorf("llmobs/export: export canceled: %w", err)
}

// spanRow is a validated span and its original input index, for row-level error
// attribution.
type spanRow struct {
	index int
	span  *transport.LLMObsSpanEvent
}

// validateSpan reports why a span must be dropped, or nil when it is valid. Kind
// and Status are checked against their known sets: an unrecognized value posts
// cleanly but lands in an unqueryable facet, so it is a row-level drop here
// rather than a silent data-quality problem at intake.
func validateSpan(e SpanEvent) *ValidationError {
	switch {
	case e.SpanID == "" || e.TraceID == "":
		return &ValidationError{Code: ErrMissingID, Reason: "missing span_id or trace_id"}
	case e.Kind == "":
		return &ValidationError{Code: ErrMissingKind, Reason: "missing kind"}
	}
	if _, ok := validKinds[e.Kind]; !ok {
		return &ValidationError{Code: ErrInvalidKind, Reason: fmt.Sprintf("invalid Kind %q", e.Kind)}
	}
	if _, ok := validStatuses[e.Status]; e.Status != "" && !ok {
		return &ValidationError{Code: ErrInvalidStatus, Reason: fmt.Sprintf("invalid Status %q", e.Status)}
	}
	return nil
}

// SubmitEvaluations exports LLM Obs evaluation metrics. Invalid rows (bad join,
// wrong value count, missing label) are dropped and reported in
// ExportResult.ValidationErrors. The input is scanned in windows of the client's
// evaluation batch size and each window is POSTed once.
func (c *Client) SubmitEvaluations(ctx context.Context, evals []EvaluationMetric, opts ...SubmitEvaluationsOption) (*ExportResult, error) {
	sc := c.resolveSubmitEvaluations(opts)
	res := &ExportResult{}
	size := c.evalBatch
	if size <= 0 {
		size = len(evals)
	}
	for start := 0; start < len(evals); start += size {
		if err := ctx.Err(); err != nil {
			return res, cancel(res, len(evals)-start, err)
		}
		end := min(start+size, len(evals))
		batch := make([]evalRow, 0, end-start)
		for i := start; i < end; i++ {
			w, reason := evals[i].lower(sc.mlApp)
			if reason != nil {
				res.ValidationErrors = append(res.ValidationErrors, ValidationError{Index: i, Code: reason.Code, Reason: reason.Reason})
				continue
			}
			batch = append(batch, evalRow{index: i, metric: w})
		}
		c.sendEvalBatch(ctx, batch, res)
	}
	failed := res.finalize()
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("llmobs/export: export canceled: %w", err)
	}
	return res, exportutil.Aggregate(failed, len(res.Requests), "llmobs/export")
}

// evalRow is a validated (lowered) metric and its original input index.
type evalRow struct {
	index  int
	metric *transport.LLMObsMetric
}

// sendEvalBatch encodes a batch of lowered metrics and POSTs it once. A metric
// that fails to encode (unmarshalable JSONValue/Metadata) is dropped as a
// row-level error and the rest are retried, so one bad row cannot fail the batch.
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
	body, err := transport.MarshalJSON(payload)
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
	r, perr := c.transport.ExportPost(ctx, transport.EndpointEvalMetric, transport.SubdomainEvalMetric, "application/json", body)
	applyResult(&rr, r, perr)
	res.Requests = append(res.Requests, rr)
}

// dropUnencodableEvals marks metrics that fail to encode as row-level errors and
// returns the ones that encode cleanly.
func dropUnencodableEvals(batch []evalRow, res *ExportResult) []evalRow {
	good := make([]evalRow, 0, len(batch))
	for _, r := range batch {
		if _, err := transport.MarshalJSON(r.metric); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  r.index,
				Code:   ErrNotEncodable,
				Reason: "evaluation is not JSON-encodable: " + err.Error(),
			})
			continue
		}
		good = append(good, r)
	}
	return good
}

// sendSpanBatch encodes and POSTs a batch, appending one RequestResult per POST.
// Spans arrive validated and stamped (see SubmitSpans). A span holding a
// non-encodable value (e.g. a non-finite metric cost) is dropped as a row-level
// error and the rest retried, so one bad row cannot fail the batch.
//
// If the encoded body exceeds the size limit and the batch has more than one
// span, the batch is bisected and recursed so individually-acceptable spans are
// not penalized for being grouped together; only a single span that is still too
// large has its input/output truncated (marking dropped_io) as a last resort.
func (c *Client) sendSpanBatch(ctx context.Context, batch []spanRow, res *ExportResult) {
	if len(batch) == 0 {
		return
	}
	body, err := marshalSpanPayload(batch)
	if err != nil {
		good := dropUnencodableSpans(batch, res)
		if len(good) == len(batch) {
			// Every row encodes alone yet the batch failed: should be impossible;
			// surface as a request error rather than silently dropping the batch.
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
			_ = cancel(res, len(batch[mid:]), err)
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
	r, perr := c.transport.ExportPost(ctx, transport.EndpointLLMSpan, transport.SubdomainLLMSpan, "application/json", body)
	applyResult(&rr, r, perr)
	res.Requests = append(res.Requests, rr)
}

// marshalSpanPayload encodes a batch as the /api/v2/llmobs body: the JSON array
// of per-span envelopes that transport.NewPushSpanEventsRequests builds for the
// live path too.
func marshalSpanPayload(batch []spanRow) ([]byte, error) {
	spans := make([]*transport.LLMObsSpanEvent, len(batch))
	for i := range batch {
		spans[i] = batch[i].span
	}
	return transport.MarshalJSON(transport.NewPushSpanEventsRequests(spans))
}

// dropUnencodableSpans marks spans that fail to encode as row-level errors and
// returns the ones that encode cleanly.
func dropUnencodableSpans(batch []spanRow, res *ExportResult) []spanRow {
	good := make([]spanRow, 0, len(batch))
	for _, r := range batch {
		if _, err := transport.MarshalJSON(r.span); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{
				Index:  r.index,
				Code:   ErrNotEncodable,
				Reason: "span is not JSON-encodable: " + err.Error(),
			})
			continue
		}
		good = append(good, r)
	}
	return good
}

// dropSpanIO truncates every span's input/output via the live path's
// illmobs.DropSpanEventIO — so the sentinel text and dropped_io collection error
// match — and re-encodes. Last resort when a single span still exceeds the size
// limit; only input/output shrink, so a span dominated by other fields may still
// be rejected by intake. The spans are export-owned copies built by toWire, so
// mutating them in place cannot touch the caller's input.
func dropSpanIO(batch []spanRow) ([]byte, error) {
	for _, r := range batch {
		illmobs.DropSpanEventIO(r.span)
	}
	return marshalSpanPayload(batch)
}

// ---- helpers ----

// stampTags fills in the tags the live path puts on every span, so a group-by
// mixing the two sources sees one population rather than a tagged and an untagged
// bucket. source carries the live value for the same reason; a caller wanting a
// different provenance sets the tag itself, which wins (see stampTag).
func stampTags(tags []string, env, version, mlApp string) []string {
	tags = stampTag(tags, "env", env)
	tags = stampTag(tags, "version", version)
	tags = stampTag(tags, "ml_app", mlApp)
	tags = stampTag(tags, illmobs.TagKeySource, illmobs.TagValueSource)
	tags = stampTag(tags, illmobs.TagKeyLanguage, illmobs.TagValueLanguage)
	tags = stampTag(tags, illmobs.TagKeyTracerVersion, tracerVersion())
	return tags
}

// stampTag ensures tags carries a non-empty key:val default. A caller-supplied
// NON-empty value for key wins and is left untouched; an empty-valued "key:" tag
// is treated as absent (dropped) so it cannot shadow a required default such as
// ml_app. It never mutates the input's backing array.
func stampTag(tags []string, key, val string) []string {
	prefix := key + ":"
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) && t != prefix {
			return tags // caller already set a non-empty value; it wins
		}
	}
	if val == "" {
		return tags // nothing to stamp
	}
	// No non-empty value present: drop any empty "key:" placeholder, then stamp val.
	out := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		if t != prefix {
			out = append(out, t)
		}
	}
	return append(out, prefix+val)
}

// replaceTag sets key:val, dropping any existing tag with the same key first. A
// blank val leaves tags untouched. Used where a structured field is the source
// of truth (e.g. session_id) and must win over a stale caller-supplied tag. It
// never mutates the input's backing array.
func replaceTag(tags []string, key, val string) []string {
	if val == "" {
		return tags
	}
	prefix := key + ":"
	out := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		if !strings.HasPrefix(t, prefix) {
			out = append(out, t)
		}
	}
	return append(out, key+":"+val)
}

func applyResult(rr *RequestResult, r transport.PostResult, err error) {
	rr.StatusCode = r.StatusCode
	rr.Attempts = r.Attempts
	rr.Retriable = r.Retriable
	rr.ResponseSnippet = exportutil.Snippet(r.Body)
	rr.Err = err
}
