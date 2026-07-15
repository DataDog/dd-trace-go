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

// MetricClient exports offline OTLP metric requests.
type MetricClient struct {
	t *rawTransport
}

// NewMetricClient builds a MetricClient for the /v1/metrics endpoint from cfg.
// On the Datadog route it adds the dd-otel-metric-config header so exponential
// histograms are emitted as distributions. The header is not added on the
// collector/Agent route (Endpoint set), which does not understand it.
func NewMetricClient(cfg Config) (*MetricClient, error) {
	var extra map[string]string
	if cfg.Endpoint == "" && cfg.APIKey != "" {
		extra = map[string]string{headerMetricConfig: metricConfigDistributions}
	}
	t, err := newRawTransport(cfg, pathMetrics, extra)
	if err != nil {
		return nil, err
	}
	return &MetricClient{t: t}, nil
}

// ExportMetrics posts each request atomically. It returns a non-nil error if any
// request failed; per-request detail is in the result.
func (c *MetricClient) ExportMetrics(ctx context.Context, requests []*metricspb.ExportMetricsServiceRequest) (*ExportResult, error) {
	return exportEach(ctx, c.t, requests, metricPartialSuccess)
}

func metricPartialSuccess(body []byte) (int64, string, error) {
	var resp metricspb.ExportMetricsServiceResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		return 0, "", err
	}
	ps := resp.GetPartialSuccess()
	return ps.GetRejectedDataPoints(), ps.GetErrorMessage(), nil
}
