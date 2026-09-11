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

// SubmitLogs submits completed OTLP log requests.
func (c *Client) SubmitLogs(ctx context.Context, requests []*logspb.ExportLogsServiceRequest) (*Result, error) {
	return submitEach(ctx, c.transport, pathLogs, requests, logPartialSuccess)
}

func logPartialSuccess(body []byte) (int64, string, error) {
	var resp logspb.ExportLogsServiceResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		return 0, "", err
	}
	ps := resp.GetPartialSuccess()
	return ps.GetRejectedLogRecords(), ps.GetErrorMessage(), nil
}
