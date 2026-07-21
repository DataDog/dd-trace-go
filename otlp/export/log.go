// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"

	"google.golang.org/protobuf/proto"

	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
)

// LogClient exports offline OTLP log requests.
type LogClient struct {
	t *rawTransport
}

// NewLogClient builds a LogClient for the /v1/logs endpoint from cfg.
func NewLogClient(cfg Config) (*LogClient, error) {
	t, err := newRawTransport(cfg, pathLogs, nil)
	if err != nil {
		return nil, err
	}
	return &LogClient{t: t}, nil
}

// ExportLogs posts each request atomically. It returns a non-nil error if any
// request failed; per-request detail is in the result.
func (c *LogClient) ExportLogs(ctx context.Context, requests []*logspb.ExportLogsServiceRequest) (*ExportResult, error) {
	return exportEach(ctx, c.t, requests, logPartialSuccess)
}

func logPartialSuccess(body []byte) (int64, string, error) {
	var resp logspb.ExportLogsServiceResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		return 0, "", err
	}
	ps := resp.GetPartialSuccess()
	return ps.GetRejectedLogRecords(), ps.GetErrorMessage(), nil
}
