// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package remoteconfig

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// truncatedBodyReader implements io.ReadCloser and always errors on Read,
// simulating a connection reset or a truncated chunked-encoding response
// mid-body — the response headers (a 200 OK) have already arrived, only the
// body transfer itself fails.
type truncatedBodyReader struct{}

func (truncatedBodyReader) Read([]byte) (int, error) {
	return 0, errors.New("simulated connection reset reading response body")
}

func (truncatedBodyReader) Close() error { return nil }

// truncatedBodyTransport answers every request with a 200 OK whose Body
// always fails on Read.
type truncatedBodyTransport struct{}

func (truncatedBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       truncatedBodyReader{},
		Header:     make(http.Header),
	}, nil
}

// TestUpdateState_TruncatedResponseBody_LogsLocallyWithoutReporting proves
// updateState's io.ReadAll(resp.Body) failure logs locally via
// internal/log.Debug but does NOT call ReportError/LogAndReportError — see
// the call site's own comment in remoteconfig.go for why: the response
// headers already arrived, so a failure reading the body is a mid-transfer
// network/transport condition (connection reset, truncated chunked
// encoding), not evidence of a dd-trace-go defect, unlike the JSON-parse
// failure a few lines below (which requires the body to have fully
// arrived).
func TestUpdateState_TruncatedResponseBody_LogsLocallyWithoutReporting(t *testing.T) {
	tp := new(log.RecordLogger)
	defer log.UseLogger(tp)()
	// log.Debug is a no-op unless the debug level is explicitly enabled,
	// unlike log.Error/Warn/Info which always print - restore the previous
	// level afterward so this doesn't leak into other tests.
	prevLevel := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(prevLevel)

	rcClient, err := newClient(ClientConfig{
		AgentURL: "http://127.0.0.1:0",
		HTTP:     &http.Client{Transport: truncatedBodyTransport{}},
	})
	require.NoError(t, err)

	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	rcClient.updateState()

	log.Flush()
	client.Flush()

	var found bool
	for _, l := range tp.Logs() {
		if strings.Contains(l, "remoteconfig: http request error: could not read the response body") {
			found = true
			break
		}
	}
	require.True(t, found, "expected a local log line for the truncated-body case, got: %v", tp.Logs())

	require.Empty(t, rt.LogMessages(), "a mid-transfer network failure must not be reported to Error Tracking")
}
