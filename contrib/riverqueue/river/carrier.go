// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/riverqueue/river/rivertype"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// metadataKey is the job metadata key holding the propagated trace context. It
// matches the "_datadog" message attribute used by the SQS and SNS integrations.
const metadataKey = "_datadog"

var (
	quotedMetadataKey = []byte(`"` + metadataKey + `"`)
	jsonNull          = []byte("null")

	errInvalidMetadata = errors.New("job metadata is not a JSON object")
)

// injectIntoParams stores the trace context of span in the metadata of every
// job in manyParams. The context is encoded once and shared by the whole batch.
// A job whose metadata is not a JSON object is left unchanged.
func injectIntoParams(span *tracer.Span, manyParams []*rivertype.JobInsertParams) error {
	if len(manyParams) == 0 {
		return nil
	}
	carrier := make(tracer.TextMapCarrier)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		return err
	}
	if len(carrier) == 0 {
		// The tracer is not running, or propagation is disabled in its config.
		return nil
	}
	encoded, err := json.Marshal(carrier)
	if err != nil {
		return err
	}
	var firstErr error
	for _, params := range manyParams {
		metadata, err := withTraceContext(params.Metadata, encoded)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		params.Metadata = metadata
	}
	return firstErr
}

// withTraceContext returns metadata with its metadataKey entry set to
// traceContext, an encoded JSON object. Metadata must be empty or a JSON object,
// whose other keys are kept.
//
// The common cases don't decode the metadata: empty metadata, which is what
// River inserts by default, and an object that has no trace context yet, which
// gets the entry spliced in after its opening brace. Only metadata that already
// carries a trace context, or contains escape sequences that could spell the
// key, is decoded and encoded again.
func withTraceContext(metadata, traceContext []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(metadata)
	if len(trimmed) == 0 || bytes.Equal(trimmed, jsonNull) {
		return spliceTraceContext(nil, traceContext), nil
	}
	if trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && !bytes.Contains(trimmed, quotedMetadataKey) && bytes.IndexByte(trimmed, '\\') < 0 {
		rest := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
		if len(rest) == 0 {
			return spliceTraceContext(nil, traceContext), nil
		}
		if rest[0] == '"' {
			out := spliceTraceContext(rest, traceContext)
			if json.Valid(out) {
				return out, nil
			}
			return nil, errInvalidMetadata
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, err
	}
	fields[metadataKey] = traceContext
	return json.Marshal(fields)
}

// spliceTraceContext returns the JSON object {"_datadog":traceContext,rest},
// where rest holds the members of another object, without its braces.
func spliceTraceContext(rest, traceContext []byte) []byte {
	out := make([]byte, 0, len(metadataKey)+len(traceContext)+len(rest)+6)
	out = append(out, '{')
	out = append(out, quotedMetadataKey...)
	out = append(out, ':')
	out = append(out, traceContext...)
	if len(rest) > 0 {
		out = append(out, ',')
		out = append(out, rest...)
	}
	return append(out, '}')
}

// extractFromMetadata returns the trace context stored in a job's metadata, or
// nil if there is none or it can't be decoded.
func extractFromMetadata(metadata []byte) *tracer.SpanContext {
	// Most jobs inserted without this middleware have no trace context; skip
	// decoding their metadata altogether.
	if !bytes.Contains(metadata, quotedMetadataKey) {
		return nil
	}
	var holder struct {
		TraceContext map[string]string `json:"_datadog"`
	}
	if err := json.Unmarshal(metadata, &holder); err != nil || len(holder.TraceContext) == 0 {
		return nil
	}
	sctx, err := tracer.Extract(tracer.TextMapCarrier(holder.TraceContext))
	if err != nil {
		return nil
	}
	return sctx
}
