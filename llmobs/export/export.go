// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/exportutil"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

const (
	droppedIOText     = "[dropped: payload too large]"
	collectionDropped = "dropped_io"
)

// SubmitSpans exports LLM Obs spans. Rows missing span_id, trace_id or kind are
// dropped and reported in ExportResult.ValidationErrors. The input is scanned in
// windows of the client's span batch size and each window is POSTed once, so
// peak memory is bounded by one batch rather than the whole input. The returned
// error is non-nil if any request failed; per-request detail is in the result.
func (c *Client) SubmitSpans(ctx context.Context, events []SpanEvent, opts ...SubmitOption) (*ExportResult, error) {
	sc := c.resolveSubmit(opts)
	res := &ExportResult{}
	size := c.spanBatch
	if size <= 0 {
		size = len(events)
	}
	for start := 0; start < len(events); start += size {
		// The caller asked to stop: return promptly rather than validating and
		// POSTing every remaining batch (each Post would just fail on the canceled
		// context and accumulate artificial request failures).
		if err := ctx.Err(); err != nil {
			res.finalize()
			return res, fmt.Errorf("llmobs/export: export canceled: %w", err)
		}
		end := min(start+size, len(events))
		batch := make([]spanRow, 0, end-start)
		for i := start; i < end; i++ {
			e := events[i]
			if reason := validateSpan(e); reason != "" {
				res.ValidationErrors = append(res.ValidationErrors, ValidationError{Index: i, Reason: reason})
				continue
			}
			ws := e.toWire(sc.service)
			ws.Tags = stampTags(ws.Tags, c.env, c.version, c.mlApp)
			// The intake reads service from a service: tag (the storage schema has no
			// top-level service field); mirror Trajectory's production payload, which
			// emits both the top-level service field and a service: tag. The resolved
			// service is authoritative, so replace any stale caller-supplied
			// service: tag rather than leaving it to disagree with ws.Service.
			ws.Tags = replaceTag(ws.Tags, "service", ws.Service)
			// The live path also emits session_id as a tag so evaluations can tag-join
			// on it, and overwrites that tag from the resolved session ID. Mirror both:
			// when SessionID is set it is the source of truth and replaces any stale
			// session_id tag, so a tag-joined evaluation cannot attach to a session
			// that disagrees with the span's declared SessionID.
			ws.Tags = replaceTag(ws.Tags, "session_id", e.SessionID)
			batch = append(batch, spanRow{index: i, span: ws})
		}
		c.sendSpanBatch(ctx, batch, res)
	}
	failed := res.finalize()
	return res, exportutil.Aggregate(failed, len(res.Requests), "llmobs/export")
}

// spanRow is a validated span and its original input index (for row-level error
// attribution). Encoding happens once per batch in sendSpanBatch, so no encoded
// bytes are retained across the whole input.
type spanRow struct {
	index int
	span  *transport.LLMObsSpanEvent
}

// validateSpan reports why a span is invalid (dropped), or "" when it is valid.
func validateSpan(e SpanEvent) string {
	switch {
	case e.SpanID == "" || e.TraceID == "":
		return "missing span_id or trace_id"
	case e.Kind == "":
		return "missing kind"
	default:
		return ""
	}
}

// SubmitEvaluations exports LLM Obs evaluation metrics. Invalid rows (bad join,
// wrong value count, missing label) are dropped and reported in
// ExportResult.ValidationErrors. The input is scanned in windows of the client's
// evaluation batch size and each window is POSTed once.
func (c *Client) SubmitEvaluations(ctx context.Context, evals []EvaluationMetric, _ ...SubmitOption) (*ExportResult, error) {
	res := &ExportResult{}
	size := c.evalBatch
	if size <= 0 {
		size = len(evals)
	}
	for start := 0; start < len(evals); start += size {
		// Stop promptly on caller cancellation instead of POSTing every remaining
		// batch against the canceled context (see SubmitSpans).
		if err := ctx.Err(); err != nil {
			res.finalize()
			return res, fmt.Errorf("llmobs/export: export canceled: %w", err)
		}
		end := min(start+size, len(evals))
		batch := make([]evalRow, 0, end-start)
		for i := start; i < end; i++ {
			w, reason := evals[i].lower(c.mlApp)
			if reason != "" {
				res.ValidationErrors = append(res.ValidationErrors, ValidationError{Index: i, Reason: reason})
				continue
			}
			batch = append(batch, evalRow{index: i, metric: w})
		}
		c.sendEvalBatch(ctx, batch, res)
	}
	failed := res.finalize()
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
	body, err := marshalJSON(payload)
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
	r, perr := c.transport.Post(ctx, transport.PathEvalMetrics, transport.SubdomainEval, "application/json", body)
	applyResult(&rr, r, perr)
	res.Requests = append(res.Requests, rr)
}

// dropUnencodableEvals marks metrics that fail to encode as row-level errors and
// returns the ones that encode cleanly.
func dropUnencodableEvals(batch []evalRow, res *ExportResult) []evalRow {
	good := make([]evalRow, 0, len(batch))
	for _, r := range batch {
		if _, err := marshalJSON(r.metric); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{Index: r.index, Reason: "evaluation is not JSON-encodable: " + err.Error()})
			continue
		}
		good = append(good, r)
	}
	return good
}

// sendSpanBatch encodes and POSTs a batch, appending one RequestResult per POST.
// The /api/v2/llmobs intake expects a JSON array of push-span-events requests
// (see internal/llmobs/transport.PushSpanEvents), so the payload is wrapped in a
// single-element array on the wire.
//
// Spans arrive validated and stamped (see SubmitSpans); this method encodes the
// batch once and POSTs it. A span holding a non-encodable value (e.g. a
// non-finite metric cost) is dropped as a row-level error and the rest retried,
// so one bad row cannot fail the batch.
//
// If the encoded body exceeds the size limit and the batch has more than one
// span, the batch is bisected and recursed so individually-acceptable spans are
// not penalized for being grouped together; only a single span that is still too
// large has its input/output truncated (marking dropped_io) as a last resort.
func (c *Client) sendSpanBatch(ctx context.Context, batch []spanRow, res *ExportResult) {
	if len(batch) == 0 {
		return
	}
	payload := spanPayload(batch)
	body, err := marshalJSON([]*transport.PushSpanEventsRequest{payload})
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
	r, perr := c.transport.Post(ctx, transport.PathLLMSpans, transport.SubdomainLLMSpans, "application/json", body)
	applyResult(&rr, r, perr)
	res.Requests = append(res.Requests, rr)
}

// spanPayload builds the intake envelope from a batch of validated spans.
func spanPayload(batch []spanRow) *transport.PushSpanEventsRequest {
	spans := make([]*transport.LLMObsSpanEvent, len(batch))
	for i := range batch {
		spans[i] = batch[i].span
	}
	return &transport.PushSpanEventsRequest{
		Stage:         "raw",
		TracerVersion: tracerVersion(),
		EventType:     "span",
		Spans:         spans,
	}
}

// dropUnencodableSpans marks spans that fail to encode as row-level errors and
// returns the ones that encode cleanly.
func dropUnencodableSpans(batch []spanRow, res *ExportResult) []spanRow {
	good := make([]spanRow, 0, len(batch))
	for _, r := range batch {
		if _, err := marshalJSON(r.span); err != nil {
			res.ValidationErrors = append(res.ValidationErrors, ValidationError{Index: r.index, Reason: "span is not JSON-encodable: " + err.Error()})
			continue
		}
		good = append(good, r)
	}
	return good
}

// dropSpanIO truncates every span's input/output (marking dropped_io) and
// re-marshals, a last resort when a single span still exceeds the size limit.
// It is best-effort: only input/output are shrunk, so a span dominated by other
// fields may still exceed the limit and be rejected by intake.
func dropSpanIO(batch []spanRow) ([]byte, error) {
	spans := make([]*transport.LLMObsSpanEvent, len(batch))
	for i := range batch {
		src := batch[i].span
		ws := *src // struct copy; Meta map is replaced below, not mutated in place
		meta := make(map[string]any, len(src.Meta))
		maps.Copy(meta, src.Meta)
		dropped := false
		if _, ok := meta["input"]; ok {
			meta["input"] = map[string]any{"value": droppedIOText}
			dropped = true
		}
		if _, ok := meta["output"]; ok {
			meta["output"] = map[string]any{"value": droppedIOText}
			dropped = true
		}
		ws.Meta = meta
		if dropped {
			ws.CollectionErrors = appendUnique(ws.CollectionErrors, collectionDropped)
		}
		spans[i] = &ws
	}
	p := &transport.PushSpanEventsRequest{Stage: "raw", TracerVersion: tracerVersion(), EventType: "span", Spans: spans}
	return marshalJSON([]*transport.PushSpanEventsRequest{p})
}

// ---- helpers ----

// marshalJSON encodes v without HTML escaping so LLM input/output content is not
// mangled, and trims the trailing newline json.Encoder appends.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func stampTags(tags []string, env, version, mlApp string) []string {
	tags = stampTag(tags, "env", env)
	tags = stampTag(tags, "version", version)
	// The live LLM Obs client stamps ml_app on every span; mirror that from the
	// client default when the caller did not supply an ml_app tag.
	tags = stampTag(tags, "ml_app", mlApp)
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

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

func applyResult(rr *RequestResult, r transport.Result, err error) {
	rr.StatusCode = r.StatusCode
	rr.Attempts = r.Attempts
	rr.Retriable = r.Retriable
	rr.ResponseSnippet = exportutil.Snippet(r.Body)
	rr.Err = err
}
