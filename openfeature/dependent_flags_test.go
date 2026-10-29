// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"errors"
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

func TestDependentFlags_NestedEvaluationAndImmediateTelemetry(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("leaf", "leaf-on", nil),
		dependencyTestFlag("middle", "middle-on", dependencyCondition("leaf", operatorOneOf, "leaf-on")),
		dependencyTestFlag("root", "root-on", dependencyCondition("middle", operatorOneOf, "middle-on")),
	)

	var reached []string
	result := evaluateConfiguredFlagWithCallbacks(config, "root", false, nil, time.Now(), experimentationExclusion{},
		func(key string, _ valueType, _ any, result evaluationResult) {
			reached = append(reached, key)
			require.NotZero(t, result.Metadata[metadataEvalTimeKey])
		})

	require.NoError(t, result.Error)
	require.Equal(t, true, result.Value)
	require.Equal(t, []string{"leaf", "middle"}, reached)
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

	var reached []string
	result := evaluateConfiguredFlagWithCallbacks(config, "root", false, map[string]any{"tier": "free"}, time.Now(), experimentationExclusion{},
		func(key string, _ valueType, _ any, _ evaluationResult) { reached = append(reached, key) })

	require.NoError(t, result.Error)
	require.Equal(t, []string{"dependency"}, reached, "memoized prerequisites emit evaluation telemetry once")
	require.Equal(t, []string{"dependency"}, dependencyExposureKeys(result), "the second rule replays the rolled-back candidate")

	root.Allocations[0].Rules = root.Allocations[0].Rules[:1]
	result = evaluateConfiguredFlagWithCallbacks(config, "root", false, map[string]any{"tier": "free"}, time.Now(), experimentationExclusion{}, nil)
	require.NoError(t, result.Error)
	require.Equal(t, of.DefaultReason, result.Reason)
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

	var reached []string
	result := evaluateConfiguredFlagWithCallbacks(config, "root", false, nil, time.Now(), experimentationExclusion{},
		func(key string, _ valueType, _ any, _ evaluationResult) { reached = append(reached, key) })
	require.NoError(t, result.Error)
	require.Equal(t, []string{"leaf", "parent"}, reached)
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
			var reached []string
			result := evaluateConfiguredFlagWithCallbacks(test.config, test.root, false, nil, time.Now(), experimentationExclusion{},
				func(key string, _ valueType, _ any, _ evaluationResult) { reached = append(reached, key) })
			require.Equal(t, of.ErrorReason, result.Reason)
			var graphErr *dependencyGraphError
			require.True(t, errors.As(result.Error, &graphErr))
			require.Equal(t, test.wantCode, graphErr.code)
			require.Equal(t, test.wantReached, reached)
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
	var events []telemetryEvent
	result := evaluateConfiguredFlagWithCallbacks(config, "root", false, nil, time.Now(), experimentationExclusion{},
		func(key string, _ valueType, _ any, result evaluationResult) {
			events = append(events, telemetryEvent{
				key:       key,
				reason:    result.Reason,
				errorCode: errorCodeFor(result.Error),
				message:   result.Error.Error(),
			})
		})
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

			var reached []string
			result := evaluateConfiguredFlagWithCallbacks(config, "root", false, nil, time.Now(), experimentationExclusion{},
				func(key string, _ valueType, _ any, _ evaluationResult) { reached = append(reached, key) })

			require.Equal(t, of.ErrorReason, result.Reason)
			require.Equal(t, []string{"leaf", "a", "b"}, reached)
			require.Empty(t, dependencyExposureKeys(result))
		})
	}
}

func TestDependentFlags_MissingTelemetryUsesContractMessage(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("root", "on", dependencyCondition("missing", operatorOneOf, "on")),
	)
	var dependencyResult evaluationResult
	evaluateConfiguredFlagWithCallbacks(config, "root", false, nil, time.Now(), experimentationExclusion{},
		func(_ string, _ valueType, _ any, result evaluationResult) { dependencyResult = result })
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
	for _, configured := range []string{"null", `"3"`, "1.5", "-1", "256"} {
		t.Run(configured, func(t *testing.T) {
			config := dependencyTestConfig(
				dependencyTestFlag("dependency", "on", nil),
				dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
			)
			require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":`+configured+`}`), &config.EvaluatorParams))
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			require.Equal(t, of.ErrorReason, result.Reason)
			require.ErrorIs(t, result.Error, errParseError)
		})
	}
}

func TestDependentFlags_InvalidConfiguredDepthIsIgnoredUntilDependencyReached(t *testing.T) {
	config := dependencyTestConfig(dependencyTestFlag("root", "on", nil))
	require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":null}`), &config.EvaluatorParams))

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Equal(t, of.StaticReason, result.Reason)
	require.True(t, result.Value.(bool))
}

func TestDependentFlags_TelemetryCallbackCannotBreakEvaluation(t *testing.T) {
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	result := evaluateConfiguredFlagWithCallbacks(config, "root", false, nil, time.Now(), experimentationExclusion{},
		func(string, valueType, any, evaluationResult) { panic("telemetry failure") })
	require.NoError(t, result.Error)
	require.True(t, result.Value.(bool))
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
	candidates, _ := result.Metadata[metadataDependencyExposuresKey].([]dependencyExposureCandidate)
	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		keys = append(keys, candidate.FlagKey)
	}
	return keys
}

func dependencyJSONConfiguration(condition string) string {
	return `{"format":"SERVER","flags":{"root":{"key":"root","enabled":true,"variationType":"BOOLEAN",` +
		`"variations":{"on":{"key":"on","value":true}},"allocations":[{"key":"allocation",` +
		`"rules":[{"conditions":[` + condition + `]}],"splits":[{"variationKey":"on","shards":[]}]}` +
		`]}}}`
}
