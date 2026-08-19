// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"

	"google.golang.org/protobuf/proto"

	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// SubmitMetrics submits completed OTLP metric requests. Direct Datadog requests
// map exponential histograms to Datadog distributions.
func (c *Client) SubmitMetrics(ctx context.Context, requests []*metricspb.ExportMetricsServiceRequest) (*Result, error) {
	return submitEach(ctx, c.transport, pathMetrics, requests, metricPartialSuccess)
}

func metricPartialSuccess(body []byte) (int64, string, error) {
	var resp metricspb.ExportMetricsServiceResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		return 0, "", err
	}
	ps := resp.GetPartialSuccess()
	return ps.GetRejectedDataPoints(), ps.GetErrorMessage(), nil
}
