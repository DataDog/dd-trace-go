// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"

	"google.golang.org/protobuf/proto"

	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// TraceClient exports offline OTLP trace requests.
type TraceClient struct {
	t *rawTransport
}

// NewTraceClient builds a TraceClient for the /v1/traces endpoint from cfg.
func NewTraceClient(cfg Config) (*TraceClient, error) {
	t, err := newRawTransport(cfg, pathTraces, nil)
	if err != nil {
		return nil, err
	}
	return &TraceClient{t: t}, nil
}

// ExportTraces posts each request atomically. It returns a non-nil error if any
// request failed; per-request detail is in the result.
func (c *TraceClient) ExportTraces(ctx context.Context, requests []*tracepb.ExportTraceServiceRequest) (*ExportResult, error) {
	return exportEach(ctx, c.t, requests, tracePartialSuccess)
}

func tracePartialSuccess(body []byte) (int64, string, error) {
	var resp tracepb.ExportTraceServiceResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		return 0, "", err
	}
	ps := resp.GetPartialSuccess()
	return ps.GetRejectedSpans(), ps.GetErrorMessage(), nil
}
