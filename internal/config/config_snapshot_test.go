// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/samplingrules"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestPrepareConfigTelemetryValueClonesBytes(t *testing.T) {
	raw := []byte("original")

	prepared, err := prepareConfigTelemetryValue(raw)
	require.NoError(t, err)
	raw[0] = 'X'

	require.Equal(t, []byte("original"), prepared)
}

func TestPrepareConfigTelemetryValueDetachesSamplingRuleNestedMaps(t *testing.T) {
	tags := map[string]string{"customer": "gold*"}
	rules := samplingrules.TraceSamplingRules(samplingrules.Rule{
		ServiceGlob: "checkout*",
		Tags:        tags,
		Rate:        0.5,
	})
	want := telemetry.SanitizeConfigValue(rules)

	prepared, err := prepareConfigTelemetryValue(rules)
	require.NoError(t, err)
	tags["customer"] = "MUTATED-SECRET"

	require.Equal(t, want, prepared)
	require.NotContains(t, prepared, "MUTATED-SECRET")
}

func TestPrepareConfigTelemetryValueRejectsUnsupportedPointers(t *testing.T) {
	prepared, err := prepareConfigTelemetryValue(new(int))

	require.Error(t, err)
	require.Nil(t, prepared)
}

type callbackStringer struct {
	mu     sync.Mutex
	called bool
}

func (s *callbackStringer) String() string {
	s.mu.Lock()
	s.called = true
	s.mu.Unlock()
	return "SENTINEL-CALLBACK"
}

func (s *callbackStringer) wasCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.called
}

func TestSetGlobalTagDropsUnsupportedTelemetryValueWithoutFormatting(t *testing.T) {
	cfg := &Config{
		globalTags: newDynamicConfig("trace_tags", map[string]any(nil), telemetry.OriginDefault, equalMap[string], nil),
	}
	value := new(callbackStringer)
	rec := new(telemetrytest.RecordClient)
	enableTelemetryForLockTest(t, rec)

	cfg.SetGlobalTag("secret", value, telemetry.OriginCode)

	require.False(t, value.wasCalled())
	require.Empty(t, rec.Configuration)
}

func TestPreparedDefaultSamplingRulesDetachBeforeSubmit(t *testing.T) {
	tags := map[string]string{"customer": "gold*"}
	rules := samplingrules.TraceSamplingRules(samplingrules.Rule{Tags: tags, Rate: 0.5})
	want := telemetry.SanitizeConfigValue(rules)
	rec := new(telemetrytest.RecordClient)
	enableTelemetryForLockTest(t, rec)

	report := prepareDefaultConfigReport("trace_sample_rules", rules)
	tags["customer"] = "MUTATED-SECRET"
	report.submit()

	require.Len(t, rec.Configuration, 1)
	require.Equal(t, want, rec.Configuration[0].Value)
	require.NotContains(t, rec.Configuration[0].Value, "MUTATED-SECRET")
	require.Equal(t, uint64(1), rec.Configuration[0].SeqID)
}

func TestPreparedConfigReportSanitizesNaNOnce(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	enableTelemetryForLockTest(t, rec)

	report := prepareConfigReport("trace_sample_rate", math.NaN(), telemetry.OriginDefault)
	report.submit()

	require.Len(t, rec.Configuration, 1)
	require.Nil(t, rec.Configuration[0].Value)
}
