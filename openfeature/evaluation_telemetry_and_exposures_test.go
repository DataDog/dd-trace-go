// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

const (
	evaluationTelemetryEnabledEnvVar = "DD_FEATURE_FLAGS_EVALUATION_TELEMETRY_ENABLED"
	exposuresEnabledEnvVar           = "DD_FEATURE_FLAGS_EXPOSURES_ENABLED"
)

// switchesHarness registers any number of providers in one process. They share
// one intake sink, metric reader, tracer, and customer exposure hook, so a test
// observes everything the process sends regardless of which provider sent it.
type switchesHarness struct {
	sink      *optOutSink
	metrics   *flagEvalMetrics
	reader    *metric.ManualReader
	tracer    mocktracer.Tracer
	providers []*DatadogProvider
	customer  []ExposureEvaluation
}

func newSwitchesHarness(t *testing.T) *switchesHarness {
	t.Helper()
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() {
		internalconfig.SetUseFreshConfig(false)
		internalconfig.CreateNew()
	})
	t.Setenv(spanEnrichmentEnvVar, "true")
	t.Setenv(flagEvalCountsEnabledEnvVar, "true")
	t.Setenv("DD_METRICS_OTEL_ENABLED", "false")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	for _, key := range []string{evaluationTelemetryEnabledEnvVar, exposuresEnabledEnvVar} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	m, reader := setupTestMetrics(t)
	return &switchesHarness{sink: &optOutSink{}, metrics: m, reader: reader, tracer: mt}
}

// newProvider builds a provider from the current environment and config.
func (h *switchesHarness) newProvider(t *testing.T, config ProviderConfig) *DatadogProvider {
	t.Helper()
	evp := newEVPClient()
	evp.httpClient = &http.Client{Transport: h.sink}
	p := newDatadogProviderWithSourceAndEVP(config, internalffe.SourceAgentless, evp)
	cleanupProvider(t, p)
	if p.flagEvalMetricsHook != nil {
		p.flagEvalMetricsHook.metrics = h.metrics
	}
	h.providers = append(h.providers, p)
	return p
}

// register builds a provider and registers it under its own OpenFeature domain.
func (h *switchesHarness) register(t *testing.T, domain string, config ProviderConfig) *of.Client {
	t.Helper()
	p := h.newProvider(t, config)
	var flags universalFlagsConfiguration
	require.NoError(t, json.Unmarshal([]byte(`{
		"format":"SERVER","environment":{"name":"test"},
		"flags":{"experiment":{"key":"experiment","enabled":true,"variationType":"BOOLEAN",
		"variations":{"on":{"key":"on","value":true},"off":{"key":"off","value":false}},
		"allocations":[
			{"key":"allocation","doLog":true,"splits":[{"variationKey":"on","serialId":42,"shards":[]}]},
			{"key":"fallback","doLog":false,"splits":[{"variationKey":"off","shards":[]}]}
		]}}
	}`), &flags))
	p.configuration = &flags
	name := t.Name() + "/" + domain
	require.NoError(t, of.SetNamedProviderAndWait(name, p))
	client := of.NewClient(name)
	client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { h.customer = append(h.customer, e) }))
	return client
}

func (h *switchesHarness) evaluate(t *testing.T, client *of.Client, subject string, attrs map[string]any) of.BooleanEvaluationDetails {
	t.Helper()
	span := tracer.StartSpan("evaluation")
	defer span.Finish()
	ctx := tracer.ContextWithSpan(context.Background(), span)
	details, err := client.BooleanValueDetails(ctx, "experiment", false, of.NewEvaluationContext(subject, attrs))
	require.NoError(t, err)
	return details
}

// flush sends everything buffered by every provider and returns the totals
// the process sent: evaluation events, evaluation metric points, enriched
// spans, and exposures.
func (h *switchesHarness) flush(t *testing.T) (events, metrics int64, enriched int, exposures []exposureEvent) {
	t.Helper()
	for _, p := range h.providers {
		if p.flagEvalLoggingWriter != nil {
			p.flagEvalLoggingWriter.drainAndFlush()
		}
		if p.exposureWriter != nil {
			_, err := p.exposureWriter.flush()
			require.NoError(t, err)
		}
	}
	for _, event := range h.sink.events {
		events += event.EvaluationCount
	}
	for _, point := range findCounter(t, collectMetrics(t, h.reader)) {
		metrics += point.Value
	}
	for _, span := range h.tracer.FinishedSpans() {
		for key := range span.Tags() {
			if strings.HasPrefix(key, "ffe_") {
				enriched++
				break
			}
		}
	}
	return events, metrics, enriched, h.sink.exposures
}

func (h *switchesHarness) assertTelemetry(t *testing.T, want int) {
	t.Helper()
	events, metrics, enriched, _ := h.flush(t)
	assert.Equal(t, int64(want), events, "evaluation events")
	assert.Equal(t, int64(want), metrics, "evaluation metric")
	assert.Equal(t, want, enriched, "enriched spans")
}

func (h *switchesHarness) exposures(t *testing.T) []exposureEvent {
	t.Helper()
	_, _, _, exposures := h.flush(t)
	return exposures
}

func TestEvaluationTelemetryAndExposuresByDefault(t *testing.T) {
	h := newSwitchesHarness(t)
	client := h.register(t, "live", ProviderConfig{})

	details := h.evaluate(t, client, "user-1", nil)

	assert.Equal(t, "on", details.Variant)
	h.assertTelemetry(t, 1)
	assert.Len(t, h.exposures(t), 1)
	require.Len(t, h.customer, 1)
	assert.True(t, h.customer[0].ShouldSend())
}

func TestEvaluationTelemetryDisabledKeepsExposures(t *testing.T) {
	h := newSwitchesHarness(t)
	client := h.register(t, "live", ProviderConfig{EvaluationTelemetryEnabled: new(false)})

	details := h.evaluate(t, client, "user-1", nil)

	assert.Equal(t, "on", details.Variant)
	h.assertTelemetry(t, 0)
	assert.Len(t, h.exposures(t), 1)
	require.Len(t, h.customer, 1)
	assert.True(t, h.customer[0].ShouldSend())
	assert.Len(t, h.tracer.FinishedSpans(), 1, "ordinary tracing is unchanged")
}

func TestExposuresDisabledKeepsEvaluationTelemetry(t *testing.T) {
	h := newSwitchesHarness(t)
	client := h.register(t, "live", ProviderConfig{ExposuresEnabled: new(false)})

	details := h.evaluate(t, client, "user-1", nil)

	assert.Equal(t, "on", details.Variant)
	assert.Equal(t, "allocation", details.FlagMetadata[metadataAllocationKey])
	h.assertTelemetry(t, 1)
	assert.Empty(t, h.exposures(t))
	assert.Nil(t, h.providers[0].exposureWriter, "no deduplication cache is written")
	require.Len(t, h.customer, 1)
	assert.False(t, h.customer[0].IsExposure())
	assert.False(t, h.customer[0].ShouldSend())
	assert.False(t, h.customer[0].IsCacheHit())
}

// An inspection through a provider with exposures disabled must not mark the
// subject as exposed: the later evaluation through the default provider still
// sends the exposure, with the same variant.
func TestInspectionDoesNotSuppressTheLaterExposure(t *testing.T) {
	h := newSwitchesHarness(t)
	peek := h.register(t, "peek", ProviderConfig{
		EvaluationTelemetryEnabled: new(false),
		ExposuresEnabled:           new(false),
	})
	live := h.register(t, "live", ProviderConfig{})

	inspected := h.evaluate(t, peek, "user-1", nil)
	h.evaluate(t, peek, "user-1", nil)
	h.assertTelemetry(t, 0)
	assert.Empty(t, h.exposures(t), "the inspection sends no exposure")

	exposed := h.evaluate(t, live, "user-1", nil)

	assert.Equal(t, inspected.Variant, exposed.Variant)
	assert.Equal(t, inspected.Value, exposed.Value)
	assert.Equal(t, inspected.FlagMetadata[metadataAllocationKey], exposed.FlagMetadata[metadataAllocationKey])
	exposures := h.exposures(t)
	require.Len(t, exposures, 1, "the live evaluation still sends the exposure")
	assert.Equal(t, "user-1", exposures[0].Subject.ID)
	assert.Equal(t, exposed.Variant, exposures[0].Variant.Key)
	require.Len(t, h.customer, 3)
	assert.False(t, h.customer[0].IsExposure())
	assert.False(t, h.customer[1].IsExposure())
	assert.True(t, h.customer[2].ShouldSend())
	h.assertTelemetry(t, 1)
}

func TestEvaluationTelemetryAndExposuresEnvironment(t *testing.T) {
	h := newSwitchesHarness(t)
	t.Setenv(evaluationTelemetryEnabledEnvVar, "false")
	t.Setenv(exposuresEnabledEnvVar, "false")
	client := h.register(t, "live", ProviderConfig{})

	details := h.evaluate(t, client, "user-1", nil)

	assert.Equal(t, "on", details.Variant)
	h.assertTelemetry(t, 0)
	assert.Empty(t, h.exposures(t))
	p := h.providers[0]
	assert.Nil(t, p.exposureWriter)
	assert.Nil(t, p.flagEvalMetricsHook)
	assert.Nil(t, p.flagEvalLoggingWriter)
	for _, hook := range p.Hooks() {
		switch hook.(type) {
		case *exposureHook, *flagEvalMetricsHook, *flagEvalLoggingHook, *spanEnrichmentHook, *evaluationTelemetryOptOutHook:
			t.Errorf("unexpected provider hook %T", hook)
		}
	}
}

func TestEvaluationTelemetryAndExposuresOptionsOverrideEnvironment(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			h := newSwitchesHarness(t)
			t.Setenv(evaluationTelemetryEnabledEnvVar, strconv.FormatBool(!enabled))
			t.Setenv(exposuresEnabledEnvVar, strconv.FormatBool(!enabled))
			client := h.register(t, "live", ProviderConfig{
				EvaluationTelemetryEnabled: new(enabled),
				ExposuresEnabled:           new(enabled),
			})

			h.evaluate(t, client, "user-1", nil)

			want := 0
			if enabled {
				want = 1
			}
			h.assertTelemetry(t, want)
			assert.Len(t, h.exposures(t), want)
		})
	}
}

func TestEvaluationTelemetryAndExposuresCapturedAtConstruction(t *testing.T) {
	h := newSwitchesHarness(t)
	t.Setenv(exposuresEnabledEnvVar, "false")
	client := h.register(t, "live", ProviderConfig{})
	t.Setenv(exposuresEnabledEnvVar, "true")
	t.Setenv(evaluationTelemetryEnabledEnvVar, "false")

	h.evaluate(t, client, "user-1", nil)

	h.assertTelemetry(t, 1)
	assert.Empty(t, h.exposures(t))
}

// Experimentation opt-out excludes allocations by their configured doLog, so a
// provider with exposures disabled assigns an opted-out subject exactly as the
// default provider does.
func TestExposuresDisabledKeepsExperimentationOptOutAssignment(t *testing.T) {
	h := newSwitchesHarness(t)
	peek := h.register(t, "peek", ProviderConfig{
		ExposuresEnabled:          new(false),
		ExperimentationOptOutMode: ExperimentationOptOutImmediate,
	})
	live := h.register(t, "live", ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutImmediate})
	optedOut := map[string]any{experimentationOptOutAtAttribute: experimentOptOutTestTime}

	inspected := h.evaluate(t, peek, "user-1", optedOut)
	exposed := h.evaluate(t, live, "user-1", optedOut)

	assert.Equal(t, "off", inspected.Variant)
	assert.Equal(t, "fallback", inspected.FlagMetadata[metadataAllocationKey])
	assert.Equal(t, exposed.Variant, inspected.Variant)
	assert.Equal(t, exposed.FlagMetadata[metadataAllocationKey], inspected.FlagMetadata[metadataAllocationKey])
	assert.Empty(t, h.exposures(t))
}
