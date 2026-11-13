// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEnvironmentID = "11111111-1111-4111-8111-111111111111"

func environmentIdentityConfig(t *testing.T, identity environment) *universalFlagsConfiguration {
	t.Helper()
	var config universalFlagsConfiguration
	require.NoError(t, json.Unmarshal([]byte(`{"format":"SERVER","createdAt":"2026-01-01T00:00:00Z",
		"flags":{"f":{"key":"f","enabled":true,"variationType":"STRING",
		"variations":{"on":{"key":"on","value":"value"}},
		"allocations":[{"key":"a","rules":[],"splits":[{"variationKey":"on","shards":[]}]}]}}}`), &config))
	config.Environment = identity
	return &config
}

func TestEnvironmentIdentityParsing(t *testing.T) {
	for _, identity := range []environment{{ID: testEnvironmentID, Name: "Production"}, {Name: "Production"}} {
		t.Run(identity.ID, func(t *testing.T) {
			config := environmentIdentityConfig(t, identity)
			raw, err := json.Marshal(config)
			require.NoError(t, err)
			var fromRC universalFlagsConfiguration
			require.NoError(t, json.Unmarshal(raw, &fromRC))
			assert.Equal(t, identity, fromRC.Environment)
			fromAPI, err := parseUFCEnvelope([]byte(`{"data":{"type":"universal-flag-configuration","id":"1","attributes":` + string(raw) + `}}`))
			require.NoError(t, err)
			assert.Equal(t, identity, fromAPI.Environment)
			provider := &DatadogProvider{configuration: fromAPI}
			result := provider.StringEvaluation(context.Background(), "f", "fallback", of.FlattenedContext{of.TargetingKey: "user"})
			assert.Equal(t, "value", result.Value)
			assert.Equal(t, identity.ID, result.FlagMetadata[metadataEnvironmentIDKey])
			assert.Equal(t, identity.Name, result.FlagMetadata[metadataEnvironmentNameKey])
		})
	}
}

func TestEnvironmentIdentityEvaluationOutcomes(t *testing.T) {
	identity := environment{ID: testEnvironmentID, Name: "Production"}
	for _, scenario := range []string{"success", "disabled", "no allocation", "missing flag", "type mismatch", "removed config", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			config := environmentIdentityConfig(t, identity)
			h := newOptOutHarness(t, ProviderConfig{})
			provider := h.provider
			provider.configuration = config
			ctx := context.Background()
			wantIdentity := &identity
			wantDefault := scenario != "success"
			switch scenario {
			case "disabled":
				config.Flags["f"].Enabled = false
			case "no allocation":
				config.Flags["f"].Allocations = nil
			case "missing flag":
				delete(config.Flags, "f")
			case "type mismatch":
				config.Flags["f"].Variations["on"].Value = true
			case "removed config":
				provider.configuration = nil
				wantIdentity = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wantIdentity = nil
			}
			_, _ = h.client.StringValueDetails(ctx, "f", "fallback", of.NewEvaluationContext("user", nil))
			provider.flagEvalLoggingWriter.drainAndFlush()
			events := h.sink.events
			require.Len(t, events, 1)
			assert.Equal(t, int64(1), events[0].EvaluationCount)
			assert.Equal(t, wantDefault, events[0].RuntimeDefault)
			assert.Equal(t, wantIdentity, events[0].Environment)
		})
	}
}

func TestEnvironmentIdentitySurvivesConfigChanges(t *testing.T) {
	identities := []environment{
		{ID: testEnvironmentID, Name: "Production"},
		{ID: testEnvironmentID, Name: "Renamed"},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "Production"},
		{Name: "Production"},
		{},
	}
	for _, degraded := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "degraded"}[degraded], func(t *testing.T) {
			writer := newFlagEvalLoggingWriter(ProviderConfig{})
			writer.ddContext.Env = "deployment-environment"
			if degraded {
				writer.aggregator.globalCap = 0
			}
			provider := &DatadogProvider{}
			for _, identity := range identities {
				provider.configuration = environmentIdentityConfig(t, identity)
				for range 2 {
					result := provider.StringEvaluation(context.Background(), "f", "fallback", of.FlattenedContext{of.TargetingKey: "user"})
					// A config change between evaluation and Finally must not relabel this result.
					provider.configuration = environmentIdentityConfig(t, environment{Name: "later"})
					newFlagEvalLoggingHook(writer).Finally(context.Background(), makeHookContext("f", "user", nil),
						makeEvalDetails(result.Variant, result.Reason, "", result.FlagMetadata), of.NewHookHints(nil))
					provider.configuration = environmentIdentityConfig(t, identity)
				}
			}
			provider.configuration = nil
			for len(writer.events) > 0 {
				writer.aggregate(<-writer.events)
			}
			events := writer.buildFlushEvents(1)
			require.Len(t, events, len(identities))
			payloads, dropped, _, err := writer.buildFlagEvalPayloads(events)
			require.NoError(t, err)
			assert.Zero(t, dropped)
			require.Len(t, payloads, 1)
			var payload struct {
				Context flagEvalDDContext `json:"context"`
				Events  []struct {
					Environment map[string]string `json:"environment"`
					Count       int64             `json:"evaluation_count"`
				} `json:"flagEvaluations"`
			}
			require.NoError(t, json.Unmarshal(payloads[0], &payload))
			assert.Equal(t, "deployment-environment", payload.Context.Env)
			seen := make(map[environment]bool)
			for _, event := range payload.Events {
				assert.Equal(t, int64(2), event.Count)
				identity := environment{ID: event.Environment["id"], Name: event.Environment["name"]}
				seen[identity] = true
				if identity.ID == "" {
					assert.NotContains(t, event.Environment, "id")
				}
				if identity == (environment{}) {
					assert.Nil(t, event.Environment)
				}
			}
			for _, identity := range identities {
				assert.True(t, seen[identity], "missing environment %+v", identity)
			}
		})
	}
}

func TestEnvironmentIdentityConcurrentRefresh(t *testing.T) {
	provider := &DatadogProvider{}
	ctx := context.Background()
	flatCtx := of.FlattenedContext{of.TargetingKey: "user"}
	identities := []environment{
		{ID: testEnvironmentID, Name: "Production"},
		{ID: testEnvironmentID, Name: "Renamed"},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "Other"},
		{Name: "Legacy"},
		{},
	}
	configs := make([]*universalFlagsConfiguration, len(identities))
	for i, identity := range identities {
		configs[i] = environmentIdentityConfig(t, identity)
	}

	// Every replacement is first evaluated concurrently, exercising cache initialization.
	for i, config := range configs {
		provider.updateConfiguration(config)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				<-start
				result := provider.StringEvaluation(ctx, "f", "fallback", flatCtx)
				assert.Equal(t, "value", result.Value)
				assert.Equal(t, identities[i].ID, result.FlagMetadata[metadataEnvironmentIDKey])
				assert.Equal(t, identities[i].Name, result.FlagMetadata[metadataEnvironmentNameKey])
				// Hook edits belong to one evaluation, not the cached metadata values.
				result.FlagMetadata[metadataEnvironmentNameKey] = "customer edit"
			})
		}
		close(start)
		wg.Wait()
	}

	// During concurrent replacement, every evaluation must carry a coherent ID/name pair.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				result := provider.StringEvaluation(ctx, "f", "fallback", flatCtx)
				got := environment{
					ID:   result.FlagMetadata[metadataEnvironmentIDKey].(string),
					Name: result.FlagMetadata[metadataEnvironmentNameKey].(string),
				}
				assert.Contains(t, identities, got)
			}
		})
	}
	for range 100 {
		for _, config := range configs {
			provider.updateConfiguration(config)
		}
	}
	wg.Wait()
}

func TestEnvironmentIdentityReusedConfiguration(t *testing.T) {
	config := environmentIdentityConfig(t, environment{ID: testEnvironmentID, Name: "Production"})
	provider := &DatadogProvider{configuration: config}
	ctx := context.Background()
	flatCtx := of.FlattenedContext{of.TargetingKey: "user"}
	before := provider.StringEvaluation(ctx, "f", "fallback", flatCtx)

	// Reusing an unpublished object for decoding must reset even a populated cache.
	provider.configuration = nil
	next := environmentIdentityConfig(t, environment{Name: "Legacy"})
	raw, err := json.Marshal(next)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, config))
	provider.configuration = config
	after := provider.StringEvaluation(ctx, "f", "fallback", flatCtx)
	assert.Equal(t, "value", after.Value)
	assert.Equal(t, "", after.FlagMetadata[metadataEnvironmentIDKey])
	assert.Equal(t, "Legacy", after.FlagMetadata[metadataEnvironmentNameKey])
	assert.Equal(t, testEnvironmentID, before.FlagMetadata[metadataEnvironmentIDKey])
	assert.Equal(t, "Production", before.FlagMetadata[metadataEnvironmentNameKey])
}
