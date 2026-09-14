// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// TestParseDecisionMaker_MalformedValue_LogsLocallyWithoutReporting proves
// the negative CONTRIBUTING.md's Core Tests bullet claims: a malformed
// decision-maker value (e.g. from an inbound _dd.p.dm propagation tag) makes
// parseDecisionMaker log locally via internal/log.Error, but does NOT call
// ReportError/LogAndReportError — see the call site's own comment in
// propagating_tags.go for why (invalid external input, not a dd-trace-go
// defect; reporting it per-request would let any upstream peer inflate an
// SDK Error Tracking issue).
func TestParseDecisionMaker_MalformedValue_LogsLocallyWithoutReporting(t *testing.T) {
	tp := new(log.RecordLogger)
	defer log.UseLogger(tp)()

	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	got := parseDecisionMaker("zz")
	assert.Equal(t, uint32(0), got)

	// internal/log.Error aggregates by default (DD_LOGGING_RATE, 60s
	// default) and only writes to the configured Logger on Flush — force it
	// now rather than depend on that timer.
	log.Flush()
	client.Flush()

	logs := tp.Logs()
	require.Len(t, logs, 1)
	assert.Contains(t, logs[0], "failed to convert decision maker to uint32")
	assert.Contains(t, logs[0], `parsing "zz"`)

	// The negative this test exists to prove: nothing reached the
	// telemetry client — no ReportError/LogAndReportError call happened.
	assert.Empty(t, rt.LogMessages())
}
