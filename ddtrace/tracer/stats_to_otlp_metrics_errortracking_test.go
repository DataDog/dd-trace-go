// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestSketchDecodeErrorsAreReportedOncePerFlushOutsideRetries(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	var attempts atomic.Int32
	var exportedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		exportedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	cfg := internalconfig.CreateNew()
	sender := &otlpStatsSender{exporter: &otlpMetricsExporter{
		transport: newOTLPTransport(server.Client(), server.URL, nil),
		protocol:  "http/json",
		cfg:       cfg,
	}}
	payload := makePayload("svc", "", "", []*pb.ClientGroupedStats{
		{Resource: "invalid-ok-and-error", OkSummary: []byte{0xff}, ErrorSummary: []byte{0xff}},
		{Resource: "another-invalid-summary", OkSummary: []byte{0xff}},
		{Resource: "valid-summary", OkSummary: encodeSketch(t, 50e6)},
	})

	require.NoError(t, sender.send(payload, 1, time.Nanosecond))
	assert.Equal(t, int32(2), attempts.Load(), "the failed transport attempt must be retried")
	assert.Contains(t, string(exportedBody), spanDurationMetricName)
	assert.Contains(t, string(exportedBody), "valid-summary")
	assert.NotContains(t, string(exportedBody), "invalid-ok-and-error")
	assert.NotContains(t, string(exportedBody), "another-invalid-summary")

	client.Flush()
	logs := capture.LogMessages()
	require.Len(t, logs, 1)
	entry := logs[0]
	assert.Equal(t, "ERROR", string(entry.Level))
	assert.Equal(t, uint32(1), entry.Count, "one representative error must be reported outside retries")
	assert.Contains(t, entry.Message, "stats_to_otlp_metrics: failed to decode sketch: error.error_type=")
	assert.NotContains(t, entry.Message, "invalid-ok-and-error")
	assert.NotContains(t, entry.Message, "another-invalid-summary")
	assert.Contains(t, entry.StackTrace, "stats.go")
	assert.Contains(t, entry.StackTrace, "(*otlpStatsSender).send")
}
