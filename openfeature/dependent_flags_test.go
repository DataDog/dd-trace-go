// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/require"
)

func TestDependentFlags_JSONConfiguration(t *testing.T) {
	var config universalFlagsConfiguration
	require.NoError(t, json.Unmarshal([]byte(`{
		"format":"SERVER",
		"evaluatorParams":{"maxDependencyDepth":3},
		"flags":{"root":{"key":"root","enabled":true,"variationType":"BOOLEAN",
			"variations":{"on":{"key":"on","value":true}},
			"allocations":[{"key":"root-allocation","rules":[{"conditions":[
				{"operator":"ONE_OF","flagEvaluation":{"key":"dependency"},"value":["on"]}
			]}],"splits":[{"variationKey":"on","shards":[]}]}
		]}}
	}`), &config))
	require.Equal(t, 3, *config.EvaluatorParams.MaxDependencyDepth)
	condition := config.Flags["root"].Allocations[0].Rules[0].Conditions[0]
	require.Equal(t, "dependency", condition.FlagEvaluation.Key)
	require.Empty(t, condition.Attribute)
}

func TestDependentFlags_JSONConditionValidation(t *testing.T) {
	tests := []struct {
		name      string
		condition string
		valid     bool
	}{
		{name: "explicit empty attribute", condition: `{"operator":"ONE_OF","attribute":"","value":["on"]}`, valid: true},
		{name: "missing source", condition: `{"operator":"ONE_OF","value":["on"]}`},
		{name: "both sources", condition: `{"operator":"ONE_OF","attribute":"","flagEvaluation":{"key":"dependency"},"value":["on"]}`},
		{name: "extra flag evaluation field", condition: `{"operator":"ONE_OF","flagEvaluation":{"key":"dependency","extra":true},"value":["on"]}`},
		{name: "non string member", condition: `{"operator":"ONE_OF","flagEvaluation":{"key":"dependency"},"value":["on",1]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var config universalFlagsConfiguration
			require.NoError(t, json.Unmarshal([]byte(dependencyJSONConfiguration(test.condition)), &config))
			_, valid := config.Flags["root"]
			require.Equal(t, test.valid, valid)
			if !test.valid {
				require.Contains(t, config.invalidFlags, "root")
			}
		})
	}
}

func TestDependentFlags_NotOneOf(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "selected", dependencyCondition("dependency", operatorNotOneOf, "off")),
	)
	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.True(t, result.Value.(bool))
	require.Equal(t, []string{"dependency"}, dependencyExposureKeys(result))
}

func TestDependentFlags_NestedEvaluationReturnsTelemetryBatch(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("leaf", "leaf-on", nil),
		dependencyTestFlag("middle", "middle-on", dependencyCondition("leaf", operatorOneOf, "leaf-on")),
		dependencyTestFlag("root", "root-on", dependencyCondition("middle", operatorOneOf, "middle-on")),
	)

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})

	require.NoError(t, result.Error)
	require.Equal(t, true, result.Value)
	require.Equal(t, []string{"leaf", "middle"}, dependencyEvaluationKeys(result))
	for _, record := range result.DependencyEvaluations {
		require.NotZero(t, record.Result.Metadata[metadataEvalTimeKey])
	}
	require.Equal(t, []string{"leaf", "middle"}, dependencyExposureKeys(result))
}

func TestDependentFlags_TransactionalExposureRollbackAndMemoReplay(t *testing.T) {
	dependency := dependencyTestFlag("dependency", "dependency-on", nil)
	root := dependencyTestFlag("root", "root-on", nil)
	root.Allocations[0].Rules = []*rule{
		{Conditions: []*condition{
			dependencyCondition("dependency", operatorOneOf, "dependency-on"),
			{Attribute: "tier", Operator: operatorOneOf, Value: []string{"paid"}},
		}},
		{Conditions: []*condition{dependencyCondition("dependency", operatorOneOf, "dependency-on")}},
	}
	config := dependencyTestConfig(dependency, root)

	result := evaluateConfiguredFlag(config, "root", false, map[string]any{"tier": "free"}, time.Now(), experimentationExclusion{})

	require.NoError(t, result.Error)
	require.Equal(t, []string{"dependency"}, dependencyEvaluationKeys(result), "memoized prerequisites return evaluation telemetry once")
	require.Equal(t, []string{"dependency"}, dependencyExposureKeys(result), "the second rule replays the rolled-back candidate")

	root.Allocations[0].Rules = root.Allocations[0].Rules[:1]
	result = evaluateConfiguredFlag(config, "root", false, map[string]any{"tier": "free"}, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Equal(t, of.DefaultReason, result.Reason)
	require.Equal(t, []string{"dependency"}, dependencyEvaluationKeys(result), "reached evaluations survive rejected rules")
	require.Empty(t, dependencyExposureKeys(result), "an unmatched root path must not commit prerequisite exposures")
}

func TestDependentFlags_ReplaysMemoizedSubtreeAfterRollback(t *testing.T) {
	leaf := dependencyTestFlag("leaf", "leaf-on", nil)
	parent := dependencyTestFlag("parent", "parent-on", dependencyCondition("leaf", operatorOneOf, "leaf-on"))
	root := dependencyTestFlag("root", "root-on", nil)
	root.Allocations[0].Rules = []*rule{
		{Conditions: []*condition{dependencyCondition("parent", operatorOneOf, "wrong")}},
		{Conditions: []*condition{dependencyCondition("parent", operatorOneOf, "parent-on")}},
	}
	config := dependencyTestConfig(leaf, parent, root)

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Equal(t, []string{"leaf", "parent"}, dependencyEvaluationKeys(result))
	require.Equal(t, []string{"leaf", "parent"}, dependencyExposureKeys(result))
}

func TestDependentFlags_RollsBackOnRootFailureAndNoAssignment(t *testing.T) {
	for _, test := range []struct {
		name      string
		variation string
		splits    []*split
		wantError bool
	}{
		{name: "root failure", variation: "missing", splits: []*split{{VariationKey: "missing", Shards: []*shard{}}}, wantError: true},
		{name: "no assignment", splits: []*split{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dependency := dependencyTestFlag("dependency", "on", nil)
			root := dependencyTestFlag("root", "selected", dependencyCondition("dependency", operatorOneOf, "on"))
			root.Allocations[0].Splits = test.splits
			config := dependencyTestConfig(dependency, root)
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			if test.wantError {
				require.Error(t, result.Error)
			} else {
				require.NoError(t, result.Error)
				require.Equal(t, of.DefaultReason, result.Reason)
			}
			require.Equal(t, []string{"dependency"}, dependencyEvaluationKeys(result), "reached evaluations are non-transactional")
			require.Empty(t, dependencyExposureKeys(result))
		})
	}
}

func TestDependentFlags_CycleMissingAndDepthErrors(t *testing.T) {
	tests := []struct {
		name        string
		config      *universalFlagsConfiguration
		root        string
		wantCode    of.ErrorCode
		wantReached []string
	}{
		{
			name: "cycle",
			config: dependencyTestConfig(
				dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on")),
				dependencyTestFlag("dependency", "dependency-on", dependencyCondition("root", operatorOneOf, "root-on")),
			),
			root: "root", wantCode: of.GeneralCode, wantReached: []string{"dependency"},
		},
		{
			name: "missing",
			config: dependencyTestConfig(
				dependencyTestFlag("root", "root-on", dependencyCondition("missing", operatorOneOf, "on")),
			),
			root: "root", wantCode: of.FlagNotFoundCode, wantReached: []string{"missing"},
		},
		{
			name: "maximum depth",
			config: dependencyTestConfigWithDepth(1,
				dependencyTestFlag("leaf", "leaf-on", nil),
				dependencyTestFlag("dependency", "dependency-on", dependencyCondition("leaf", operatorOneOf, "leaf-on")),
				dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on")),
			),
			root: "root", wantCode: of.GeneralCode, wantReached: []string{"leaf", "dependency"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := evaluateConfiguredFlag(test.config, test.root, false, nil, time.Now(), experimentationExclusion{})
			require.Equal(t, of.ErrorReason, result.Reason)
			var graphErr *dependencyGraphError
			require.True(t, errors.As(result.Error, &graphErr))
			require.Equal(t, test.wantCode, graphErr.code)
			require.Equal(t, test.wantReached, dependencyEvaluationKeys(result))
			require.Empty(t, dependencyExposureKeys(result))
		})
	}
}

func TestDependentFlags_OverDepthEmitsRejectedNodeBeforeAncestors(t *testing.T) {
	config := dependencyTestConfigWithDepth(1,
		dependencyTestFlag("leaf", "leaf-on", nil),
		dependencyTestFlag("dependency", "dependency-on", dependencyCondition("leaf", operatorOneOf, "leaf-on")),
		dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on")),
	)
	type telemetryEvent struct {
		key       string
		reason    of.Reason
		errorCode of.ErrorCode
		message   string
	}
	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	events := make([]telemetryEvent, 0, len(result.DependencyEvaluations))
	for _, record := range result.DependencyEvaluations {
		events = append(events, telemetryEvent{
			key:       record.FlagKey,
			reason:    record.Result.Reason,
			errorCode: errorCodeFor(record.Result.Error),
			message:   record.Result.Error.Error(),
		})
	}
	require.Equal(t, of.ErrorReason, result.Reason)
	require.Equal(t, []telemetryEvent{
		{key: "leaf", reason: of.ErrorReason, errorCode: of.GeneralCode, message: "maximum flag dependency depth exceeded"},
		{key: "dependency", reason: of.ErrorReason, errorCode: of.GeneralCode, message: "maximum flag dependency depth exceeded"},
	}, events)
}

func TestDependentFlags_MemoizedSubtreeDepthFailureDoesNotReemitMemoizedNode(t *testing.T) {
	for _, rootDependencies := range [][]string{{"a", "b"}, {"b", "a"}} {
		t.Run(rootDependencies[0]+" first", func(t *testing.T) {
			leaf := dependencyTestFlag("leaf", "on", nil)
			a := dependencyTestFlag("a", "on", dependencyCondition("leaf", operatorOneOf, "on"))
			b := dependencyTestFlag("b", "on", dependencyCondition("a", operatorOneOf, "on"))
			root := dependencyTestFlag("root", "on", nil)
			root.Allocations[0].Rules = []*rule{{Conditions: []*condition{
				dependencyCondition(rootDependencies[0], operatorOneOf, "on"),
				dependencyCondition(rootDependencies[1], operatorOneOf, "on"),
			}}}
			config := dependencyTestConfigWithDepth(2, leaf, a, b, root)

			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})

			require.Equal(t, of.ErrorReason, result.Reason)
			require.Equal(t, []string{"leaf", "a", "b"}, dependencyEvaluationKeys(result))
			require.Empty(t, dependencyExposureKeys(result))
		})
	}
}

func TestDependentFlags_MissingTelemetryUsesContractMessage(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("root", "on", dependencyCondition("missing", operatorOneOf, "on")),
	)
	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.Len(t, result.DependencyEvaluations, 1)
	dependencyResult := result.DependencyEvaluations[0].Result
	require.EqualError(t, dependencyResult.Error, "prerequisite flag is missing")
	require.Equal(t, of.ErrorReason, dependencyResult.Reason)
	require.Equal(t, of.FlagNotFoundCode, errorCodeFor(dependencyResult.Error))
}

func TestDependentFlags_DefaultAndZeroDepth(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error, "missing evaluatorParams uses the default")

	zero := 0
	config.EvaluatorParams.MaxDependencyDepth = &zero
	result = evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.Equal(t, of.ErrorReason, result.Reason)
	var graphErr *dependencyGraphError
	require.ErrorAs(t, result.Error, &graphErr)
	require.Equal(t, of.GeneralCode, graphErr.code)
}

func TestDependentFlags_InvalidConfiguredDepthIsParseError(t *testing.T) {
	for _, configured := range []string{
		"null", `"3"`, "1.5", "-1", "256",
		"254.99999999999999", "255.00000000000001", "1e-400",
	} {
		t.Run(configured, func(t *testing.T) {
			config := dependencyTestConfig(
				dependencyTestFlag("dependency", "on", nil),
				dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
			)
			require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":`+configured+`}`), &config.EvaluatorParams))
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			require.Equal(t, of.ErrorReason, result.Reason)
			require.ErrorIs(t, result.Error, errParseError)
			require.Equal(t, []string{"dependency"}, dependencyEvaluationKeys(result))
			require.Len(t, result.DependencyEvaluations, 1)
			record := result.DependencyEvaluations[0]
			require.Equal(t, valueTypeBoolean, record.FlagType)
			require.Equal(t, false, record.DefaultValue)
			require.Equal(t, false, record.Result.Value)
			require.Equal(t, of.ErrorReason, record.Result.Reason)
			require.Equal(t, of.ParseErrorCode, errorCodeFor(record.Result.Error))
			require.ErrorIs(t, record.Result.Error, errParseError)
			require.Empty(t, dependencyExposureKeys(result))
		})
	}
}

func TestDependentFlags_ExactIntegerDepthForms(t *testing.T) {
	for configured, want := range map[string]int{
		"3.0":    3,
		"1e2":    100,
		"2.55e2": 255,
		"-0":     0,
	} {
		t.Run(configured, func(t *testing.T) {
			var params evaluatorParams
			require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":`+configured+`}`), &params))
			require.False(t, params.maxDependencyDepthInvalid)
			require.NotNil(t, params.MaxDependencyDepth)
			require.Equal(t, want, *params.MaxDependencyDepth)
		})
	}
}

func TestDependentFlags_InvalidConfiguredDepthPublishesDependencyBatch(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":null}`), &config.EvaluatorParams))
	h.provider.mu.Lock()
	h.provider.configuration = config
	h.provider.mu.Unlock()

	_, err := h.client.BooleanValueDetails(context.Background(), "root", false,
		of.NewEvaluationContext("subject", nil))
	require.Error(t, err)
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	require.Len(t, h.sink.events, 2)
	keys := make([]string, 0, len(h.sink.events))
	for _, event := range h.sink.events {
		keys = append(keys, event.Flag.Key)
	}
	require.ElementsMatch(t, []string{"dependency", "root"}, keys)
	var metricCount int64
	for _, point := range findCounter(t, collectMetrics(t, h.reader)) {
		metricCount += point.Value
	}
	require.Equal(t, int64(2), metricCount)
	require.Empty(t, h.sink.exposures)
}

func TestDependentFlags_InvalidConfiguredDepthIsIgnoredUntilDependencyReached(t *testing.T) {
	config := dependencyTestConfig(dependencyTestFlag("root", "on", nil))
	require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":null}`), &config.EvaluatorParams))

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Equal(t, of.StaticReason, result.Reason)
	require.True(t, result.Value.(bool))
}

func TestDependentFlags_ReturnedTelemetryIsDetachedAndPublisherCannotBreakEvaluation(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("leaf", "on", nil),
		dependencyTestFlag("dependency", "on", dependencyCondition("leaf", operatorOneOf, "on")),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.True(t, result.Value.(bool))
	require.Len(t, result.DependencyEvaluations, 2)
	var published []string
	publishDependencyEvaluationBatch(result.DependencyEvaluations, func(record dependencyEvaluationRecord) {
		published = append(published, record.FlagKey)
		record.Result.Metadata[metadataAllocationKey] = "mutated"
		record.Result.Value = false
		if record.FlagKey == "leaf" {
			panic("telemetry failure")
		}
	})
	require.Equal(t, []string{"leaf", "dependency"}, published, "one failed publisher call must not suppress later records")
	require.True(t, result.DependencyEvaluations[0].Result.Value.(bool))
	require.Equal(t, "leaf-allocation", result.DependencyEvaluations[0].Result.Metadata[metadataAllocationKey])
	require.Equal(t, []string{"leaf", "dependency"}, dependencyExposureKeys(result))
	_, ok := result.Metadata[metadataDependencyExposuresKey].(string)
	require.True(t, ok, "private dependency exposures must cross FlagMetadata as a scalar")
}

func TestDependentFlags_NestedJSONTelemetryIsDeeplyDetached(t *testing.T) {
	configuredValue := map[string]any{
		"nested":  map[string]any{"leaf": "original"},
		"any":     []any{map[string]any{"leaf": "original"}, []any{"original"}},
		"strings": []string{"original", "stable"},
	}
	expected := cloneTelemetryValue(configuredValue)
	jsonFlag := func(key string) *flag {
		flag := dependencyTestFlag(key, "json-on", nil)
		flag.VariationType = valueTypeJSON
		flag.Variations["json-on"].Value = configuredValue
		return flag
	}
	config := dependencyTestConfig(
		jsonFlag("json-a"),
		jsonFlag("json-b"),
		dependencyTestFlag("root", "on",
			dependencyCondition("json-a", operatorOneOf, "json-on"),
			dependencyCondition("json-b", operatorOneOf, "json-on")),
	)

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Len(t, result.DependencyEvaluations, 2)
	var secondPublished any
	publishDependencyEvaluationBatch(result.DependencyEvaluations, func(record dependencyEvaluationRecord) {
		if record.FlagKey == "json-b" {
			secondPublished = record.Result.Value
			return
		}
		value := record.Result.Value.(map[string]any)
		value["nested"].(map[string]any)["leaf"] = "mutated"
		value["any"].([]any)[0].(map[string]any)["leaf"] = "mutated"
		value["any"].([]any)[1].([]any)[0] = "mutated"
		value["strings"].([]string)[0] = "mutated"
		panic("publisher failure after mutation")
	})

	require.Equal(t, expected, result.DependencyEvaluations[0].Result.Value)
	require.Equal(t, expected, result.DependencyEvaluations[1].Result.Value)
	require.Equal(t, expected, secondPublished)
	require.Equal(t, expected, config.Flags["json-a"].Variations["json-on"].Value)
	require.Equal(t, expected, config.Flags["json-b"].Variations["json-on"].Value)
}

func TestDependentFlags_DependencyErrorsAreDetached(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("parent", "on", dependencyCondition("missing", operatorOneOf, "on")),
		dependencyTestFlag("root", "on", dependencyCondition("parent", operatorOneOf, "on")),
	)
	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.Error(t, result.Error)
	require.Len(t, result.DependencyEvaluations, 2)

	rootErr := result.Error.(*dependencyGraphError)
	missingErr := result.DependencyEvaluations[0].Result.Error.(*dependencyGraphError)
	parentErr := result.DependencyEvaluations[1].Result.Error.(*dependencyGraphError)
	require.NotSame(t, rootErr, parentErr)
	require.NotSame(t, parentErr.cause, missingErr)

	missingErr.message = "publisher mutation"
	parentErr.message = "publisher mutation"
	require.NotEqual(t, "publisher mutation", rootErr.message)
	require.NotEqual(t, "publisher mutation", parentErr.cause.(*dependencyGraphError).message)
}

func TestDependentFlags_ParallelRootsKeepReturnedBatchesIndependent(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	const evaluations = 32
	var wg sync.WaitGroup
	errs := make(chan error, evaluations)
	for range evaluations {
		wg.Go(func() {
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			if result.Error != nil {
				errs <- result.Error
				return
			}
			if keys := dependencyEvaluationKeys(result); len(keys) != 1 || keys[0] != "dependency" {
				errs <- errors.New("unexpected dependency evaluation batch")
				return
			}
			result.DependencyEvaluations[0].Result.Metadata[metadataAllocationKey] = "caller mutation"
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestDependentFlags_WideGraphDeduplicatesTelemetry(t *testing.T) {
	const dependencyCount = 512
	flags := make([]*flag, 0, dependencyCount+1)
	conditions := make([]*condition, 0, dependencyCount)
	for i := range dependencyCount {
		key := fmt.Sprintf("dependency-%d", i)
		flags = append(flags, dependencyTestFlag(key, "on", nil))
		conditions = append(conditions, dependencyCondition(key, operatorOneOf, "on"))
	}
	root := dependencyTestFlag("root", "on", nil)
	root.Allocations[0].Rules = []*rule{{Conditions: conditions}}
	flags = append(flags, root)

	result := evaluateConfiguredFlag(dependencyTestConfig(flags...), "root", false, nil, time.Now(), experimentationExclusion{})

	require.NoError(t, result.Error)
	require.Len(t, result.DependencyEvaluations, dependencyCount)
	encoded, ok := result.Metadata[metadataDependencyExposuresKey].(string)
	require.True(t, ok)
	exposures := decodeDependencyExposureCandidates(encoded)
	require.Len(t, exposures, dependencyCount)
	for i := range dependencyCount {
		key := fmt.Sprintf("dependency-%d", i)
		require.Equal(t, key, result.DependencyEvaluations[i].FlagKey)
		require.Equal(t, key, exposures[i].FlagKey)
	}
}

func BenchmarkDependentFlags_WideGraph(b *testing.B) {
	const dependencyCount = 512
	flags := make([]*flag, 0, dependencyCount+1)
	conditions := make([]*condition, 0, dependencyCount)
	for i := range dependencyCount {
		key := fmt.Sprintf("dependency-%d", i)
		flags = append(flags, dependencyTestFlag(key, "on", nil))
		conditions = append(conditions, dependencyCondition(key, operatorOneOf, "on"))
	}
	root := dependencyTestFlag("root", "on", nil)
	root.Allocations[0].Rules = []*rule{{Conditions: conditions}}
	flags = append(flags, root)
	config := dependencyTestConfig(flags...)
	now := time.Now()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result := evaluateConfiguredFlag(config, "root", false, nil, now, experimentationExclusion{})
		exposures := decodeDependencyExposureCandidates(result.Metadata[metadataDependencyExposuresKey].(string))
		if result.Error != nil || len(result.DependencyEvaluations) != dependencyCount || len(exposures) != dependencyCount {
			b.Fatalf("unexpected wide-graph result: error=%v evaluations=%d exposures=%d", result.Error, len(result.DependencyEvaluations), len(exposures))
		}
	}
}

func TestDependentFlags_ProviderTelemetryAndExposureIntegration(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	h.provider.mu.Lock()
	h.provider.configuration = dependencyTestConfig(
		dependencyTestFlag("leaf", "leaf-on", nil),
		dependencyTestFlag("middle", "middle-on", dependencyCondition("leaf", operatorOneOf, "leaf-on")),
		dependencyTestFlag("root", "root-on", dependencyCondition("middle", operatorOneOf, "middle-on")),
	)
	h.provider.mu.Unlock()

	details, err := h.client.BooleanValueDetails(context.Background(), "root", false,
		of.NewEvaluationContext("subject", map[string]any{"tier": "paid"}))
	require.NoError(t, err)
	require.True(t, details.Value)

	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
	require.Len(t, h.sink.events, 3)
	evaluationKeys := make([]string, 0, len(h.sink.events))
	for _, event := range h.sink.events {
		evaluationKeys = append(evaluationKeys, event.Flag.Key)
	}
	require.ElementsMatch(t, []string{"leaf", "middle", "root"}, evaluationKeys)
	var metricCount int64
	for _, point := range findCounter(t, collectMetrics(t, h.reader)) {
		metricCount += point.Value
	}
	require.Equal(t, int64(3), metricCount)
	require.Len(t, h.sink.exposures, 3)
	exposureKeys := make([]string, 0, len(h.sink.exposures))
	for _, exposure := range h.sink.exposures {
		exposureKeys = append(exposureKeys, exposure.Flag.Key)
	}
	require.Equal(t, []string{"leaf", "middle", "root"}, exposureKeys)
}

func TestDependentFlags_PublicObjectTypeMismatchDoesNotTraverseDependencies(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	h.provider.mu.Lock()
	h.provider.configuration = dependencyTestConfig(
		dependencyTestFlag("dependency", "dependency-on", nil),
		dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on")),
	)
	h.provider.mu.Unlock()

	defaultValue := map[string]any{"fallback": true}
	details, err := h.client.ObjectValueDetails(context.Background(), "root", defaultValue,
		of.NewEvaluationContext("subject", nil))
	require.Error(t, err)
	require.Equal(t, defaultValue, details.Value)
	require.Equal(t, of.TypeMismatchCode, details.ErrorCode)

	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
	require.Len(t, h.sink.events, 1)
	require.Equal(t, "root", h.sink.events[0].Flag.Key)
	require.Empty(t, h.sink.exposures)
}

func TestDependentFlags_InvalidRootActivationPrecedesTypeMismatch(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutFutureExperimentsOnly})
	root := dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on"))
	root.invalidActivation = true
	h.provider.mu.Lock()
	h.provider.configuration = dependencyTestConfig(
		dependencyTestFlag("dependency", "dependency-on", nil),
		root,
	)
	h.provider.mu.Unlock()

	details, err := h.client.StringValueDetails(context.Background(), "root", "fallback",
		of.NewEvaluationContext("subject", map[string]any{
			experimentationOptOutAtAttribute: experimentOptOutTestTime,
		}))
	require.Error(t, err)
	require.Equal(t, "fallback", details.Value)
	require.Equal(t, of.ParseErrorCode, details.ErrorCode)

	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
	require.Len(t, h.sink.events, 1)
	require.Equal(t, "root", h.sink.events[0].Flag.Key)
	require.Empty(t, h.sink.exposures)
}

func TestDependentFlags_OptOutSuppressesDependencyErrorTelemetry(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	h.provider.mu.Lock()
	h.provider.configuration = dependencyTestConfig(
		dependencyTestFlag("root", "on", dependencyCondition("missing", operatorOneOf, "on")),
	)
	h.provider.mu.Unlock()

	_, err := h.client.BooleanValueDetails(context.Background(), "root", false,
		of.NewEvaluationContext("subject", map[string]any{optOutTestParameter: true}))
	require.Error(t, err)
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	require.Empty(t, h.sink.events)
	var metricCount int64
	for _, point := range findCounter(t, collectMetrics(t, h.reader)) {
		metricCount += point.Value
	}
	require.Zero(t, metricCount)
}

func dependencyTestConfig(flags ...*flag) *universalFlagsConfiguration {
	config := &universalFlagsConfiguration{Flags: make(map[string]*flag, len(flags)), invalidFlags: make(map[string]error)}
	for _, flag := range flags {
		config.Flags[flag.Key] = flag
	}
	return config
}

func TestIndependentEvaluationDoesNotAllocateDependencyResolver(t *testing.T) {
	config := dependencyTestConfig(&flag{
		Key: "root", Enabled: true, VariationType: valueTypeBoolean,
		Variations: map[string]*variant{},
	})
	prepareDependencyConditions(config)

	allocations := testing.AllocsPerRun(100, func() {
		result := evaluateConfiguredFlag(config, "root", false, nil, time.Time{}, experimentationExclusion{})
		if result.Error != nil || len(result.DependencyEvaluations) != 0 {
			t.Fatalf("unexpected independent result: error=%v dependencyEvaluations=%d", result.Error, len(result.DependencyEvaluations))
		}
	})
	require.Zero(t, allocations)
}

func dependencyTestConfigWithDepth(depth int, flags ...*flag) *universalFlagsConfiguration {
	config := dependencyTestConfig(flags...)
	config.EvaluatorParams.MaxDependencyDepth = &depth
	return config
}

func dependencyTestFlag(key, variantKey string, conditions ...*condition) *flag {
	rules := []*rule(nil)
	if len(conditions) > 0 && conditions[0] != nil {
		rules = []*rule{{Conditions: conditions}}
	}
	return &flag{
		Key: key, Enabled: true, VariationType: valueTypeBoolean,
		Variations: map[string]*variant{variantKey: {Key: variantKey, Value: true}},
		Allocations: []*allocation{{
			Key: key + "-allocation", Rules: rules,
			Splits: []*split{{VariationKey: variantKey, Shards: []*shard{}}},
		}},
	}
}

func dependencyCondition(key string, operator conditionOperator, variants ...string) *condition {
	return &condition{FlagEvaluation: &flagEvaluation{Key: key}, Operator: operator, Value: variants}
}

func dependencyExposureKeys(result evaluationResult) []string {
	encoded, _ := result.Metadata[metadataDependencyExposuresKey].(string)
	candidates := decodeDependencyExposureCandidates(encoded)
	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		keys = append(keys, candidate.FlagKey)
	}
	return keys
}

func dependencyEvaluationKeys(result evaluationResult) []string {
	keys := make([]string, 0, len(result.DependencyEvaluations))
	for _, evaluation := range result.DependencyEvaluations {
		keys = append(keys, evaluation.FlagKey)
	}
	return keys
}

func dependencyJSONConfiguration(condition string) string {
	return `{"format":"SERVER","flags":{"root":{"key":"root","enabled":true,"variationType":"BOOLEAN",` +
		`"variations":{"on":{"key":"on","value":true}},"allocations":[{"key":"allocation",` +
		`"rules":[{"conditions":[` + condition + `]}],"splits":[{"variationKey":"on","shards":[]}]}` +
		`]}}}`
}
