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
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/otlp/export"
)

type discardTransport struct{}

func (discardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func BenchmarkOTLPExportSubmitTraces(b *testing.B) {
	c, err := export.NewClient(
		export.WithDatadogIntake("datadoghq.com", testAPIKey),
		export.WithHTTPClient(&http.Client{Transport: discardTransport{}}),
	)
	if err != nil {
		b.Fatal(err)
	}
	reqs := make([]*tracepb.ExportTraceServiceRequest, 50)
	for i := range reqs {
		reqs[i] = sampleTrace()
	}
	requestBytes := 0
	for _, req := range reqs {
		requestBytes += proto.Size(req)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(requestBytes))
	for b.Loop() {
		if _, err := c.SubmitTraces(ctx, reqs); err != nil {
			b.Fatal(err)
		}
	}
}
