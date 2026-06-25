// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	i "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

func TestBuildEvaluation(t *testing.T) {
	tests := []struct {
		name     string
		evalCtx  of.EvaluationContext
		details  of.InterfaceEvaluationDetails
		expected *i.FeatureFlagEvaluation
	}{
		{
			name:    "experiment with subject",
			evalCtx: of.NewEvaluationContext("user-123", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: true,
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "experiment-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant: "v1",
						FlagMetadata: of.FlagMetadata{
							metadataSerialIDKey: uint32(42),
							metadataDoLogKey:    true,
						},
					},
				},
			},
			expected: &i.FeatureFlagEvaluation{
				FlagKey:  "experiment-flag",
				SerialID: uint32Ptr(42),
				Subject:  "user-123",
			},
		},
		{
			name:    "experiment without subject logging",
			evalCtx: of.NewEvaluationContext("user-456", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: true,
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "no-log-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant: "v2",
						FlagMetadata: of.FlagMetadata{
							metadataSerialIDKey: uint32(101),
							metadataDoLogKey:    false,
						},
					},
				},
			},
			expected: &i.FeatureFlagEvaluation{
				FlagKey:  "no-log-flag",
				SerialID: uint32Ptr(101),
			},
		},
		{
			name:    "experiment without doLog metadata",
			evalCtx: of.NewEvaluationContext("user-789", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: true,
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "missing-do-log-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant: "v3",
						FlagMetadata: of.FlagMetadata{
							metadataSerialIDKey: uint32(7),
						},
					},
				},
			},
			expected: &i.FeatureFlagEvaluation{
				FlagKey:  "missing-do-log-flag",
				SerialID: uint32Ptr(7),
			},
		},
		{
			name:    "malformed serial id",
			evalCtx: of.NewEvaluationContext("user-000", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: true,
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "bad-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant: "v1",
						FlagMetadata: of.FlagMetadata{
							metadataSerialIDKey: "42",
							metadataDoLogKey:    true,
						},
					},
				},
			},
			expected: nil,
		},
		{
			name:    "runtime default",
			evalCtx: of.NewEvaluationContext("", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: "default-val",
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "default-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant: "",
					},
				},
			},
			expected: &i.FeatureFlagEvaluation{
				FlagKey:      "default-flag",
				DefaultValue: "default-val",
			},
		},
		{
			name:    "non-default without serial id",
			evalCtx: of.NewEvaluationContext("user-999", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: true,
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "plain-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant: "v1",
					},
				},
			},
			expected: nil,
		},
		{
			// When an error occurs the SDK sets ErrorCode and leaves Variant empty.
			// buildEvaluation sees Variant=="" and records the default value that
			// was returned to the caller, matching runtime-default behaviour.
			name:    "error evaluation with empty variant",
			evalCtx: of.NewEvaluationContext("user-111", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: "fallback",
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "error-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant:   "",
						ErrorCode: of.GeneralCode,
					},
				},
			},
			expected: &i.FeatureFlagEvaluation{
				FlagKey:      "error-flag",
				DefaultValue: "fallback",
			},
		},
		{
			// Type mismatches are special: the provider can resolve a real variant
			// and carry serial metadata before the typed OpenFeature API returns
			// the caller default. Span enrichment must follow the value returned
			// to the application, not the stale assignment metadata.
			name:    "type mismatch with serial id records runtime default",
			evalCtx: of.NewEvaluationContext("user-222", map[string]any{}),
			details: of.InterfaceEvaluationDetails{
				Value: "fallback",
				EvaluationDetails: of.EvaluationDetails{
					FlagKey: "typed-flag",
					ResolutionDetail: of.ResolutionDetail{
						Variant:   "configured",
						ErrorCode: of.TypeMismatchCode,
						FlagMetadata: of.FlagMetadata{
							metadataSerialIDKey: uint32(202),
							metadataDoLogKey:    true,
						},
					},
				},
			},
			expected: &i.FeatureFlagEvaluation{
				FlagKey:      "typed-flag",
				DefaultValue: "fallback",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildEvaluation(tt.evalCtx, tt.details)
			if tt.expected == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.expected.FlagKey, got.FlagKey)
			assert.Equal(t, tt.expected.Subject, got.Subject)
			assert.Equal(t, tt.expected.DefaultValue, got.DefaultValue)
			if tt.expected.SerialID == nil {
				assert.Nil(t, got.SerialID)
				return
			}
			require.NotNil(t, got.SerialID)
			assert.Equal(t, *tt.expected.SerialID, *got.SerialID)
		})
	}
}

func TestSpanEnrichment_Integration(t *testing.T) {
	t.Setenv(spanEnrichmentEnvVar, "true")

	mt := mocktracer.Start()
	defer mt.Stop()

	provider := newDatadogProvider(ProviderConfig{})
	status := processConfigUpdate(provider, "datadog/2/ASM_FEATURES/test/config", []byte(`{
		"createdAt":"2026-01-01T00:00:00Z",
		"format":"SERVER",
		"environment":{"name":"test"},
		"flags":{
			"experiment-flag":{
				"key":"experiment-flag",
				"enabled":true,
				"variationType":"BOOLEAN",
				"variations":{
					"on":{"key":"on","value":true},
					"off":{"key":"off","value":false}
				},
				"allocations":[{
					"key":"us-rollout",
					"doLog":true,
					"rules":[{"conditions":[{"operator":"ONE_OF","attribute":"country","value":["US"]}]}],
					"splits":[{"variationKey":"on","serialId":42,"shards":[]}]
				}]
			},
			"default-flag":{
				"key":"default-flag",
				"enabled":true,
				"variationType":"STRING",
				"variations":{"configured":{"key":"configured","value":"configured-val"}},
				"allocations":[{
					"key":"uk-rollout",
					"doLog":true,
					"rules":[{"conditions":[{"operator":"ONE_OF","attribute":"country","value":["UK"]}]}],
					"splits":[{"variationKey":"configured","serialId":101,"shards":[]}]
				}]
			}
		}
	}`))
	require.Equal(t, rc.ApplyStateAcknowledged, status.State)

	domain := "span-enrichment-test-app"
	require.NoError(t, of.SetNamedProviderAndWait(domain, provider))
	client := of.NewClient(domain)

	rootSpan := tracer.StartSpan("http.request")
	rootCtx := tracer.ContextWithSpan(context.Background(), rootSpan)
	childSpan := tracer.StartSpan("db.query", tracer.ChildOf(rootSpan.Context()))
	childCtx := tracer.ContextWithSpan(rootCtx, childSpan)

	evalCtx := of.NewEvaluationContext("user-123", map[string]any{"country": "US"})
	gotBool, err := client.BooleanValue(childCtx, "experiment-flag", false, evalCtx)
	require.NoError(t, err)
	assert.True(t, gotBool)

	gotString, err := client.StringValue(childCtx, "default-flag", "default-val", evalCtx)
	require.NoError(t, err)
	assert.Equal(t, "default-val", gotString)

	childSpan.Finish()
	rootSpan.Finish()

	finished := mt.FinishedSpans()
	require.Len(t, finished, 2)

	var rootMock, childMock *mocktracer.Span
	for _, s := range finished {
		if s.OperationName() == "http.request" {
			rootMock = s
		} else if s.OperationName() == "db.query" {
			childMock = s
		}
	}
	require.NotNil(t, rootMock)
	require.NotNil(t, childMock)

	for k := range childMock.Tags() {
		assert.NotContains(t, k, "ffe_")
	}

	tags := rootMock.Tags()
	assert.Contains(t, tags, "ffe_flags_enc")
	assert.Contains(t, tags, "ffe_subjects_enc")
	assert.Contains(t, tags, "ffe_runtime_defaults")

	flagsEnc, ok := tags["ffe_flags_enc"].(string)
	require.True(t, ok)
	flagsDec, err := base64.StdEncoding.DecodeString(flagsEnc)
	require.NoError(t, err)
	assert.Equal(t, []byte{42}, flagsDec)

	subjectsEnc, ok := tags["ffe_subjects_enc"].(string)
	require.True(t, ok)
	var subjects map[string]string
	require.NoError(t, json.Unmarshal([]byte(subjectsEnc), &subjects))
	expectedUser123Enc := base64.StdEncoding.EncodeToString([]byte{42})
	user123Hash := "fcdec6df4d44dbc637c7c5b58efface52a7f8a88535423430255be0bb89bedd8"
	assert.Equal(t, map[string]string{user123Hash: expectedUser123Enc}, subjects)

	defaultsEnc, ok := tags["ffe_runtime_defaults"].(string)
	require.True(t, ok)
	var defaults map[string]string
	require.NoError(t, json.Unmarshal([]byte(defaultsEnc), &defaults))
	assert.Equal(t, map[string]string{"default-flag": "default-val"}, defaults)
}

// setupEnrichmentProvider creates a minimal provider with a single boolean flag
// assigned to a US-only allocation (serialId=42, doLog=true) and registers it
// under the given OpenFeature domain name.
func setupEnrichmentProvider(t *testing.T, domain string) *of.Client {
	t.Helper()
	provider := newDatadogProvider(ProviderConfig{})
	status := processConfigUpdate(provider, "datadog/2/ASM_FEATURES/test/config", []byte(`{
		"createdAt":"2026-01-01T00:00:00Z",
		"format":"SERVER",
		"environment":{"name":"test"},
		"flags":{
			"experiment-flag":{
				"key":"experiment-flag",
				"enabled":true,
				"variationType":"BOOLEAN",
				"variations":{
					"on":{"key":"on","value":true},
					"off":{"key":"off","value":false}
				},
				"allocations":[{
					"key":"us-rollout",
					"doLog":true,
					"rules":[{"conditions":[{"operator":"ONE_OF","attribute":"country","value":["US"]}]}],
					"splits":[{"variationKey":"on","serialId":42,"shards":[]}]
				}]
			}
		}
	}`))
	require.Equal(t, rc.ApplyStateAcknowledged, status.State)
	require.NoError(t, of.SetNamedProviderAndWait(domain, provider))
	return of.NewClient(domain)
}

func TestSpanEnrichment_NoSpanInContext(t *testing.T) {
	t.Setenv(spanEnrichmentEnvVar, "true")

	mt := mocktracer.Start()
	defer mt.Stop()

	client := setupEnrichmentProvider(t, "span-enrichment-no-span")
	evalCtx := of.NewEvaluationContext("user-123", map[string]any{"country": "US"})

	// context.Background() carries no span; hook must return early without panicking.
	got, err := client.BooleanValue(context.Background(), "experiment-flag", false, evalCtx)
	require.NoError(t, err)
	assert.True(t, got)

	// No spans created, so nothing to check for ffe tags.
	assert.Empty(t, mt.FinishedSpans())
}

func TestSpanEnrichment_AfterRootFinished(t *testing.T) {
	t.Setenv(spanEnrichmentEnvVar, "true")

	mt := mocktracer.Start()
	defer mt.Stop()

	client := setupEnrichmentProvider(t, "span-enrichment-after-finished")
	evalCtx := of.NewEvaluationContext("user-123", map[string]any{"country": "US"})

	rootSpan := tracer.StartSpan("http.request")
	rootSpan.Finish()
	rootCtx := tracer.ContextWithSpan(context.Background(), rootSpan)

	// Evaluate after the root span is already finished – must not panic.
	got, err := client.BooleanValue(rootCtx, "experiment-flag", false, evalCtx)
	require.NoError(t, err)
	assert.True(t, got)

	finished := mt.FinishedSpans()
	require.Len(t, finished, 1)
	// ffe tags must not appear on an already-finished span.
	for k := range finished[0].Tags() {
		assert.NotContains(t, k, "ffe_")
	}
}

func uint32Ptr(v uint32) *uint32 { return &v }
