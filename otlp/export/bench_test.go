// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/otlp/export"
)

// discardTransport accepts every request and retains nothing, so the benchmark
// measures marshal/POST-assembly cost without network or bookkeeping noise.
type discardTransport struct{}

func (discardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func BenchmarkExportTraces(b *testing.B) {
	c, err := export.NewTraceClient(export.Config{
		Site: "datadoghq.com", APIKey: "k",
		HTTPClient: &http.Client{Transport: discardTransport{}},
	})
	if err != nil {
		b.Fatal(err)
	}
	reqs := make([]*tracepb.ExportTraceServiceRequest, 50)
	for i := range reqs {
		reqs[i] = sampleTrace()
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.ExportTraces(ctx, reqs); err != nil {
			b.Fatal(err)
		}
	}
}
