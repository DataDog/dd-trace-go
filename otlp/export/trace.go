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

// SubmitTraces submits completed OTLP trace requests.
func (c *Client) SubmitTraces(ctx context.Context, requests []*tracepb.ExportTraceServiceRequest) (*Result, error) {
	return submitEach(ctx, c.transport, pathTraces, requests, tracePartialSuccess)
}

func tracePartialSuccess(body []byte) (int64, string, error) {
	var resp tracepb.ExportTraceServiceResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		return 0, "", err
	}
	ps := resp.GetPartialSuccess()
	return ps.GetRejectedSpans(), ps.GetErrorMessage(), nil
}
