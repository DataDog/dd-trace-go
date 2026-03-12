// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package configtelemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestReportDefaultSeqID(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()

	ReportDefault("DD_SERVICE", "default-value")

	require.Len(t, rec.Configuration, 1)
	assert.Equal(t, defaultSeqID, rec.Configuration[0].SeqID,
		"ReportDefault must always use the fixed default sequence ID")
	assert.Equal(t, telemetry.OriginDefault, rec.Configuration[0].Origin)
}

func TestReportSeqIDIsAboveDefault(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()

	Report("DD_SERVICE", "value", telemetry.OriginEnvVar)

	require.Len(t, rec.Configuration, 1)
	assert.Greater(t, rec.Configuration[0].SeqID, defaultSeqID,
		"Report must produce a sequence ID strictly greater than the default")
}

func TestReportWithIDSeqIDIsAboveDefault(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()

	ReportWithID("DD_SERVICE", "value", telemetry.OriginLocalStableConfig, "cfg-123")

	require.Len(t, rec.Configuration, 1)
	assert.Greater(t, rec.Configuration[0].SeqID, defaultSeqID,
		"ReportWithID must produce a sequence ID strictly greater than the default")
}

func TestHigherPrioritySourceHasHigherSeqID(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()

	// Simulate the same key being reported from two sources in ascending priority order.
	// The backend uses SeqID to determine which value wins, so the higher-priority
	// source must always produce a higher SeqID.
	ReportWithID("DD_SERVICE", "local-value", telemetry.OriginLocalStableConfig, "local-123")
	ReportWithID("DD_SERVICE", "managed-value", telemetry.OriginManagedStableConfig, "managed-456")

	require.Len(t, rec.Configuration, 2)
	assert.Greater(t, rec.Configuration[1].SeqID, rec.Configuration[0].SeqID,
		"a config key reported from a higher-priority source must have a higher SeqID than a lower-priority one")
}

func TestReportDefaultDoesNotIncrementSeqID(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()

	Report("DD_SERVICE", "v1", telemetry.OriginEnvVar)
	seqAfterReport := rec.Configuration[len(rec.Configuration)-1].SeqID

	ReportDefault("DD_ENV", "default")
	ReportDefault("DD_VERSION", "default")

	// Defaults must not advance the counter — subsequent Report calls should
	// continue incrementing from where they left off before the defaults.
	Report("DD_TRACE_DEBUG", "true", telemetry.OriginEnvVar)
	seqAfterSecondReport := rec.Configuration[len(rec.Configuration)-1].SeqID

	assert.Equal(t, seqAfterReport+1, seqAfterSecondReport,
		"ReportDefault must not increment the shared sequence ID counter")
}
