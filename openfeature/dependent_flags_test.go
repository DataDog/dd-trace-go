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
	"strings"
	"sync"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
	require.JSONEq(t, `{"maxDependencyDepth":3}`, string(config.EvaluatorParams))
	require.Equal(t, 3, configuredMaxDependencyDepth(&config))
	condition := config.Flags["root"].Allocations[0].Rules[0].Conditions[0]
	require.Equal(t, "dependency", condition.FlagEvaluation.Key)
	require.Empty(t, condition.Attribute)
}

func TestDependentFlags_NonObjectEvaluatorParamsUseDefaults(t *testing.T) {
	for _, raw := range []string{`null`, `42`, `"future"`, `true`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			var config universalFlagsConfiguration
			require.NoError(t, json.Unmarshal([]byte(`{"format":"SERVER","evaluatorParams":`+raw+`,"flags":{}}`), &config))
			require.JSONEq(t, raw, string(config.EvaluatorParams))
		})
	}
}

func TestDependentFlags_EvaluatorParamsRemainOptionalAndExtensible(t *testing.T) {
	var config universalFlagsConfiguration
	require.NoError(t, json.Unmarshal([]byte(`{
		"format":"SERVER",
		"evaluatorParams":{"futureOption":{"enabled":true},"maxDependencyDepth":3},
		"flags":{}
	}`), &config))

	encoded, err := json.Marshal(&config)
	require.NoError(t, err)
	var roundTrip struct {
		EvaluatorParams json.RawMessage `json:"evaluatorParams"`
	}
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.JSONEq(t, `{"futureOption":{"enabled":true},"maxDependencyDepth":3}`, string(roundTrip.EvaluatorParams))

	var withoutParams universalFlagsConfiguration
	require.NoError(t, json.Unmarshal([]byte(`{"format":"SERVER","flags":{}}`), &withoutParams))
	require.Empty(t, withoutParams.EvaluatorParams)
	encoded, err = json.Marshal(&withoutParams)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "evaluatorParams")
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
		{name: "extra flag evaluation field", condition: `{"operator":"ONE_OF","flagEvaluation":{"key":"dependency","extra":true},"value":["on"]}`, valid: true},
		{name: "empty flag evaluation key", condition: `{"operator":"ONE_OF","flagEvaluation":{"key":""},"value":["on"]}`},
		{name: "null flag evaluation key", condition: `{"operator":"ONE_OF","flagEvaluation":{"key":null},"value":["on"]}`},
		{name: "non string flag evaluation key", condition: `{"operator":"ONE_OF","flagEvaluation":{"key":42},"value":["on"]}`},
		{name: "dependency matches operator", condition: `{"operator":"MATCHES","flagEvaluation":{"key":"dependency"},"value":"on"}`},
		{name: "dependency not matches operator", condition: `{"operator":"NOT_MATCHES","flagEvaluation":{"key":"dependency"},"value":"on"}`},
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
				result := evaluateConfiguredFlag(&config, "root", false, nil, time.Now(), experimentationExclusion{})
				require.Equal(t, of.ErrorReason, result.Reason)
				require.Equal(t, of.ParseErrorCode, errorCodeFor(result.Error))
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
	require.Equal(t, []string{"middle", "leaf"}, dependencyEvaluationKeys(result))
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

func TestDependentFlags_MemoizationDeduplicatesActualEvaluation(t *testing.T) {
	dependency := dependencyTestFlag("dependency", "on", nil)
	root := dependencyTestFlag("root", "on", nil)
	root.Allocations[0].Rules = []*rule{{Conditions: []*condition{
		dependencyCondition("dependency", operatorOneOf, "on"),
		dependencyCondition("dependency", operatorOneOf, "on"),
	}}}
	counts := make(map[string]int)
	result := evaluateConfiguredFlagWithCounter(dependencyTestConfig(dependency, root), "root", func(key string) {
		counts[key]++
	})

	require.NoError(t, result.Error)
	require.Equal(t, 1, counts["dependency"], "memo lookup must bypass actual prerequisite evaluation")
	require.Equal(t, []string{"dependency"}, dependencyEvaluationKeys(result))
	require.Equal(t, []string{"dependency"}, dependencyExposureKeys(result))
}

func TestDependentFlags_NoAssignmentAbortsWaterfall(t *testing.T) {
	noAssignment := dependencyTestFlag("no-assignment", "on", nil)
	noAssignment.Allocations[0].Splits = nil
	later := dependencyTestFlag("later", "on", nil)
	firstRoot := dependencyTestFlag("root", "on", dependencyCondition("no-assignment", operatorOneOf, "on"))
	secondRoot := dependencyTestFlag("root", "on", dependencyCondition("later", operatorOneOf, "on"))
	firstRoot.Allocations = append(firstRoot.Allocations, secondRoot.Allocations[0])
	counts := make(map[string]int)
	result := evaluateConfiguredFlagWithCounter(dependencyTestConfig(noAssignment, later, firstRoot), "root", func(key string) {
		counts[key]++
	})

	require.Equal(t, of.ErrorReason, result.Reason)
	require.Equal(t, of.GeneralCode, errorCodeFor(result.Error))
	require.Equal(t, 1, counts["no-assignment"])
	require.Zero(t, counts["later"], "a no-assignment prerequisite must abort before later allocations")
	require.Equal(t, []string{"no-assignment"}, dependencyEvaluationKeys(result))
	require.Equal(t, of.GeneralCode, errorCodeFor(result.DependencyEvaluations[0].Result.Error))
	require.Empty(t, dependencyExposureKeys(result))
}

func TestDependentFlags_EarlierSiblingTelemetrySurvivesLaterFailure(t *testing.T) {
	root := dependencyTestFlag("root", "on", nil)
	root.Allocations[0].Rules = []*rule{{Conditions: []*condition{
		dependencyCondition("successful", operatorOneOf, "on"),
		dependencyCondition("missing", operatorOneOf, "on"),
	}}}
	result := evaluateConfiguredFlag(dependencyTestConfig(
		dependencyTestFlag("successful", "on", nil), root,
	), "root", false, nil, time.Now(), experimentationExclusion{})

	require.Equal(t, of.ErrorReason, result.Reason)
	require.Equal(t, of.GeneralCode, errorCodeFor(result.Error))
	require.Equal(t, []string{"successful", "missing"}, dependencyEvaluationKeys(result))
	require.NoError(t, result.DependencyEvaluations[0].Result.Error)
	require.Equal(t, of.FlagNotFoundCode, errorCodeFor(result.DependencyEvaluations[1].Result.Error))
	require.Empty(t, dependencyExposureKeys(result), "all dependency exposures roll back on graph failure")
}

func TestDependentFlags_RootGeneralPreservesDependencyError(t *testing.T) {
	sharded := dependencyTestFlag("needs-targeting-key", "on", nil)
	sharded.Allocations[0].Splits[0].Shards = []*shard{{
		Salt: "salt", TotalShards: 100, Ranges: []*shardRange{{Start: 0, End: 100}},
	}}
	for _, test := range []struct {
		name           string
		dependencyKey  string
		flags          []*flag
		dependencyCode of.ErrorCode
	}{
		{name: "missing", dependencyKey: "missing", dependencyCode: of.FlagNotFoundCode},
		{name: "targeting key missing", dependencyKey: "needs-targeting-key", flags: []*flag{sharded}, dependencyCode: of.TargetingKeyMissingCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := dependencyTestFlag("root", "on", dependencyCondition(test.dependencyKey, operatorOneOf, "on"))
			result := evaluateConfiguredFlag(dependencyTestConfig(append(test.flags, root)...), "root", false, nil, time.Now(), experimentationExclusion{})

			require.Equal(t, of.ErrorReason, result.Reason)
			require.Equal(t, of.GeneralCode, errorCodeFor(result.Error), "root must not leak the prerequisite-specific code")
			require.Len(t, result.DependencyEvaluations, 1)
			require.Equal(t, test.dependencyKey, result.DependencyEvaluations[0].FlagKey)
			require.Equal(t, test.dependencyCode, errorCodeFor(result.DependencyEvaluations[0].Result.Error))
			require.NotEmpty(t, result.DependencyEvaluations[0].Result.Error.Error())
			require.Empty(t, dependencyExposureKeys(result))
		})
	}
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
	require.Equal(t, []string{"parent", "leaf"}, dependencyEvaluationKeys(result))
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
			root: "root", wantCode: of.GeneralCode, wantReached: []string{"missing"},
		},
		{
			name: "maximum depth",
			config: dependencyTestConfigWithDepth(1,
				dependencyTestFlag("leaf", "leaf-on", nil),
				dependencyTestFlag("dependency", "dependency-on", dependencyCondition("leaf", operatorOneOf, "leaf-on")),
				dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on")),
			),
			root: "root", wantCode: of.GeneralCode, wantReached: []string{"dependency", "leaf"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := evaluateConfiguredFlag(test.config, test.root, false, nil, time.Now(), experimentationExclusion{})
			require.Equal(t, of.ErrorReason, result.Reason)
			var graphErr *dependencyGraphError
			require.True(t, errors.As(result.Error, &graphErr))
			require.Equal(t, test.wantCode, graphErr.code)
			require.Contains(t, toResolutionError(result.Error).Error(), string(test.wantCode)+":")
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
		{key: "dependency", reason: of.ErrorReason, errorCode: of.GeneralCode, message: "prerequisite flag \"leaf\" failed: maximum flag dependency depth exceeded"},
		{key: "leaf", reason: of.ErrorReason, errorCode: of.GeneralCode, message: "maximum flag dependency depth exceeded"},
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
			want := []string{"a", "leaf", "b"}
			if rootDependencies[0] == "b" {
				want = []string{"b", "a", "leaf"}
			}
			require.Equal(t, want, dependencyEvaluationKeys(result))
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

	config.EvaluatorParams = json.RawMessage(`{"maxDependencyDepth":0}`)
	result = evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.Equal(t, of.ErrorReason, result.Reason)
	var graphErr *dependencyGraphError
	require.ErrorAs(t, result.Error, &graphErr)
	require.Equal(t, of.GeneralCode, graphErr.code)
}

func TestDependentFlags_DefaultDepthBoundary(t *testing.T) {
	for _, test := range []struct {
		edges     int
		wantError bool
	}{
		{edges: 10},
		{edges: 11, wantError: true},
	} {
		t.Run(fmt.Sprintf("edges_%d", test.edges), func(t *testing.T) {
			config := dependencyChainConfig(test.edges)
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			require.Len(t, result.DependencyEvaluations, test.edges)
			if test.wantError {
				require.Equal(t, of.ErrorReason, result.Reason)
				require.Equal(t, of.GeneralCode, errorCodeFor(result.Error))
				require.Empty(t, dependencyExposureKeys(result))
				return
			}
			require.NoError(t, result.Error)
			require.Len(t, dependencyExposureKeys(result), test.edges)
		})
	}
}

func TestDependentFlags_InvalidConfiguredDepthLogsAndUsesDefault(t *testing.T) {
	for _, configured := range []string{
		"null", `"3"`, "1.5", "-1", "256",
		"254.99999999999999", "255.00000000000001", "1e-400",
		"1e1000000", "1e-1000000",
	} {
		t.Run(configured, func(t *testing.T) {
			logger := withLogLevel(t, log.LevelWarn)
			config := dependencyTestConfig(
				dependencyTestFlag("dependency", "on", nil),
				dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
			)
			require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":`+configured+`}`), &config.EvaluatorParams))
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			secondResult := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			log.Flush()
			require.NoError(t, result.Error)
			require.NoError(t, secondResult.Error)
			require.Equal(t, of.TargetingMatchReason, result.Reason)
			require.Equal(t, []string{"dependency"}, dependencyEvaluationKeys(result))
			require.Len(t, result.DependencyEvaluations, 1)
			record := result.DependencyEvaluations[0]
			require.Equal(t, valueTypeBoolean, record.FlagType)
			require.Equal(t, false, record.DefaultValue)
			require.Equal(t, true, record.Result.Value)
			require.NoError(t, record.Result.Error)
			require.Equal(t, []string{"dependency"}, dependencyExposureKeys(result))
			require.Equal(t, 2, logger.countContaining("maxDependencyDepth must be an integer from 0 through 255; using default 10"), "each root evaluation emits its own diagnostic")
		})
	}
}

func TestDependentFlags_InvalidDepthDiagnosticIsNotCached(t *testing.T) {
	logger := withLogLevel(t, log.LevelWarn)
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":null}`), &config.EvaluatorParams))
	copied := *config

	for _, candidate := range []*universalFlagsConfiguration{config, &copied, config, &copied} {
		result := evaluateConfiguredFlag(candidate, "root", false, nil, time.Now(), experimentationExclusion{})
		require.NoError(t, result.Error)
		require.True(t, result.Value.(bool))
	}
	log.Flush()
	require.Equal(t, 4, logger.countContaining("maxDependencyDepth must be an integer from 0 through 255; using default 10"))
}

func TestDependentFlags_ExactIntegerDepthForms(t *testing.T) {
	for configured, want := range map[string]int{
		"3.0":    3,
		"1e2":    100,
		"2.55e2": 255,
		"-0":     0,
	} {
		t.Run(configured, func(t *testing.T) {
			config := &universalFlagsConfiguration{
				EvaluatorParams: json.RawMessage(`{"maxDependencyDepth":` + configured + `}`),
			}
			require.Equal(t, want, configuredMaxDependencyDepth(config))
		})
	}
}

func TestParseMaxDependencyDepthBoundsAdversarialExponents(t *testing.T) {
	tests := []struct {
		value string
		want  int
		ok    bool
	}{
		{value: "1e1000000"},
		{value: "1e-1000000"},
		{value: "255e+000000000000000000000", want: 255, ok: true},
		{value: "255000e-000000000000000003", want: 255, ok: true},
		{value: "0e" + strings.Repeat("9", 100_000), ok: true},
		{value: "1e" + strings.Repeat("9", 100_000)},
		{value: "1e-" + strings.Repeat("9", 100_000)},
		{value: strings.Repeat("9", 100_000)},
	}
	for _, tt := range tests {
		got, ok := parseMaxDependencyDepth([]byte(tt.value))
		require.Equal(t, tt.ok, ok, "value prefix %.32q", tt.value)
		require.Equal(t, tt.want, got, "value prefix %.32q", tt.value)
	}
}

func BenchmarkParseMaxDependencyDepthAdversarialExponent(b *testing.B) {
	for _, size := range []int{7, 100_000} {
		b.Run(fmt.Sprintf("digits_%d", size), func(b *testing.B) {
			raw := []byte("1e" + strings.Repeat("9", size))
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			for b.Loop() {
				_, _ = parseMaxDependencyDepth(raw)
			}
		})
	}
}

func TestDependentFlags_InvalidConfiguredDepthPublishesSuccessfulDependencyBatch(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	config := dependencyTestConfig(
		dependencyTestFlag("dependency", "on", nil),
		dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
	)
	require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":null}`), &config.EvaluatorParams))
	h.provider.mu.Lock()
	h.provider.configuration = config
	h.provider.mu.Unlock()

	details, err := h.client.BooleanValueDetails(context.Background(), "root", false,
		of.NewEvaluationContext("subject", nil))
	require.NoError(t, err)
	require.True(t, details.Value)
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
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
	require.Len(t, h.sink.exposures, 2)
}

func TestDependentFlags_InvalidConfiguredDepthIsReadOnceForIndependentRoot(t *testing.T) {
	logger := withLogLevel(t, log.LevelWarn)
	config := dependencyTestConfig(dependencyTestFlag("root", "on", nil))
	require.NoError(t, json.Unmarshal([]byte(`{"maxDependencyDepth":null}`), &config.EvaluatorParams))

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Equal(t, of.StaticReason, result.Reason)
	require.True(t, result.Value.(bool))
	log.Flush()
	require.Equal(t, 1, logger.countContaining("maxDependencyDepth"), "each root evaluation resolves the option once")
}

func TestDependentFlags_MalformedOptionsObjectsLogAndFallBack(t *testing.T) {
	for _, raw := range []string{`null`, `42`, `"future"`, `true`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			logger := withLogLevel(t, log.LevelWarn)
			config := dependencyTestConfig(
				dependencyTestFlag("dependency", "on", nil),
				dependencyTestFlag("root", "on", dependencyCondition("dependency", operatorOneOf, "on")),
			)
			config.EvaluatorParams = json.RawMessage(raw)
			result := evaluateConfiguredFlag(config, "root", false, nil, time.Now(), experimentationExclusion{})
			log.Flush()
			require.NoError(t, result.Error)
			require.True(t, result.Value.(bool))
			require.Equal(t, 1, logger.countContaining("maxDependencyDepth must be an integer from 0 through 255; using default 10"))
		})
	}
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
	dropped := publishDependencyEvaluationBatch(result.DependencyEvaluations, func(record dependencyEvaluationRecord) {
		published = append(published, record.FlagKey)
		record.Result.Metadata[metadataAllocationKey] = "mutated"
		record.Result.Value = false
		if record.FlagKey == "dependency" {
			panic("telemetry failure")
		}
	})
	require.Equal(t, int64(1), dropped)
	require.Equal(t, []string{"dependency", "leaf"}, published, "one failed publisher call must not suppress later records")
	require.True(t, result.DependencyEvaluations[1].Result.Value.(bool))
	require.Equal(t, "leaf-allocation", result.DependencyEvaluations[1].Result.Metadata[metadataAllocationKey])
	require.Equal(t, []string{"leaf", "dependency"}, dependencyExposureKeys(result))
	_, ok := result.Metadata[metadataDependencyExposuresKey].(string)
	require.True(t, ok, "private dependency exposures must cross FlagMetadata as a scalar")
}

func TestDependentFlags_PublisherPanicsAreCountedAndDoNotStopBatch(t *testing.T) {
	records := make([]dependencyEvaluationRecord, 4)
	for i := range records {
		records[i].FlagKey = fmt.Sprintf("dependency-%d", i)
	}
	var published []string
	dropped := publishDependencyEvaluationBatch(records, func(record dependencyEvaluationRecord) {
		if record.FlagKey == "dependency-0" || record.FlagKey == "dependency-2" {
			panic("private publisher value")
		}
		published = append(published, record.FlagKey)
	})
	require.Equal(t, int64(2), dropped)
	require.Equal(t, []string{"dependency-1", "dependency-3"}, published)
}

func TestDependentFlags_PublisherPanicWarningIsSanitizedAndDeduplicated(t *testing.T) {
	logger := withLogLevel(t, log.LevelWarn)
	provider := &DatadogProvider{}
	provider.reportDependencyPublisherPanics(1)
	provider.reportDependencyPublisherPanics(2)
	require.Equal(t, 1, logger.countContaining("dependency evaluation publisher panicked"))
	require.Zero(t, logger.countContaining("private publisher value"))
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
	parentErr := result.DependencyEvaluations[0].Result.Error.(*dependencyGraphError)
	missingErr := result.DependencyEvaluations[1].Result.Error.(*dependencyGraphError)
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

func TestDependentFlags_ConsentIsSnapshottedAtDependencyEvaluation(t *testing.T) {
	for _, consent := range []bool{false, true} {
		t.Run(fmt.Sprintf("consent_%t", consent), func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{})
			config := dependencyTestConfig(
				dependencyTestFlag("dependency", "dependency-on", nil),
				dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on")),
			)
			config.ObserveFullEvaluationData = consent
			h.provider.mu.Lock()
			h.provider.configuration = config
			h.provider.mu.Unlock()

			_, err := h.client.BooleanValue(context.Background(), "root", false,
				of.NewEvaluationContext("private-subject", map[string]any{"private": "value"}))
			require.NoError(t, err)

			// A later configuration must not change the consent already stamped on either
			// the returned prerequisite batch or the root event waiting to be drained.
			replacement := dependencyTestConfig()
			replacement.ObserveFullEvaluationData = !consent
			h.provider.mu.Lock()
			h.provider.configuration = replacement
			h.provider.mu.Unlock()
			h.provider.flagEvalLoggingWriter.drainAndFlush()
			require.Len(t, h.sink.events, 2)
			for _, event := range h.sink.events {
				if consent {
					require.Equal(t, "private-subject", event.TargetingKey)
					require.NotNil(t, event.Context)
				} else {
					require.NotEqual(t, "private-subject", event.TargetingKey)
					require.Nil(t, event.Context)
				}
			}
		})
	}
}

func TestDependentFlags_PerPrerequisiteDoLogAndRollback(t *testing.T) {
	for _, dependencyDoLog := range []bool{false, true} {
		for _, rootDoLog := range []bool{false, true} {
			name := fmt.Sprintf("dependency_%t_root_%t", dependencyDoLog, rootDoLog)
			t.Run(name, func(t *testing.T) {
				h := newOptOutHarness(t, ProviderConfig{})
				dependency := dependencyTestFlag("dependency", "dependency-on", nil)
				dependency.Allocations[0].DoLog = new(dependencyDoLog)
				root := dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on"))
				root.Allocations[0].DoLog = new(rootDoLog)
				h.provider.mu.Lock()
				h.provider.configuration = dependencyTestConfig(dependency, root)
				h.provider.mu.Unlock()

				_, err := h.client.BooleanValue(context.Background(), "root", false, of.NewEvaluationContext("subject", nil))
				require.NoError(t, err)
				h.provider.exposureWriter.flush()
				keys := make([]string, 0, len(h.sink.exposures))
				for _, exposure := range h.sink.exposures {
					keys = append(keys, exposure.Flag.Key)
				}
				want := make([]string, 0, 2)
				if dependencyDoLog {
					want = append(want, "dependency")
				}
				if rootDoLog {
					want = append(want, "root")
				}
				require.Equal(t, want, keys)

				h.sink.exposures = nil
				root.Allocations[0].Rules[0].Conditions = append(root.Allocations[0].Rules[0].Conditions,
					&condition{Attribute: "tier", Operator: operatorOneOf, Value: []string{"paid"}})
				_, err = h.client.BooleanValue(context.Background(), "root", false,
					of.NewEvaluationContext("other-subject", map[string]any{"tier": "free"}))
				require.NoError(t, err)
				h.provider.exposureWriter.flush()
				require.Empty(t, h.sink.exposures, "a rejected root path rolls back every prerequisite exposure")
			})
		}
	}
}

func TestDependentFlags_MalformedPrerequisitePropagation(t *testing.T) {
	for _, transitive := range []bool{false, true} {
		name := "direct"
		if transitive {
			name = "transitive"
		}
		t.Run(name, func(t *testing.T) {
			h := newOptOutHarness(t, ProviderConfig{})
			rootDependency := "malformed"
			flags := []*flag{dependencyTestFlag("root", "root-on", nil)}
			if transitive {
				rootDependency = "parent"
				flags = append(flags, dependencyTestFlag("parent", "parent-on", dependencyCondition("malformed", operatorOneOf, "on")))
			}
			flags[0].Allocations[0].Rules = []*rule{{Conditions: []*condition{dependencyCondition(rootDependency, operatorOneOf, "on")}}}
			config := dependencyTestConfig(flags...)
			config.invalidFlags["malformed"] = fmt.Errorf("%w: malformed prerequisite", errParseError)
			h.provider.mu.Lock()
			h.provider.configuration = config
			h.provider.mu.Unlock()

			details, err := h.client.BooleanValueDetails(context.Background(), "root", false, of.NewEvaluationContext("subject", nil))
			require.Error(t, err)
			require.Equal(t, of.GeneralCode, details.ErrorCode)
			h.provider.flagEvalLoggingWriter.drainAndFlush()
			h.provider.exposureWriter.flush()
			wantCodes := map[string]string{"malformed": string(of.ParseErrorCode), "root": string(of.GeneralCode)}
			if transitive {
				wantCodes["parent"] = string(of.GeneralCode)
			}
			require.Len(t, h.sink.events, len(wantCodes))
			for _, event := range h.sink.events {
				require.NotNil(t, event.Error)
				require.Equal(t, wantCodes[event.Flag.Key], event.Error.Message)
			}
			require.Empty(t, h.sink.exposures)
		})
	}
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

func TestDependentFlags_IntegerFlagCanBeEvaluatedAsFloat(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	root := dependencyTestFlag("root", "root-on", dependencyCondition("dependency", operatorOneOf, "dependency-on"))
	root.VariationType = valueTypeInteger
	root.Variations["root-on"].Value = int64(7)
	h.provider.mu.Lock()
	h.provider.configuration = dependencyTestConfig(
		dependencyTestFlag("dependency", "dependency-on", nil),
		root,
	)
	h.provider.mu.Unlock()

	details, err := h.client.FloatValueDetails(context.Background(), "root", 0,
		of.NewEvaluationContext("subject", nil))
	require.NoError(t, err)
	require.Equal(t, 7.0, details.Value)
	require.Equal(t, of.TargetingMatchReason, details.Reason)

	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
	require.Len(t, h.sink.events, 2)
	require.ElementsMatch(t, []string{"dependency", "root"}, []string{
		h.sink.events[0].Flag.Key,
		h.sink.events[1].Flag.Key,
	})
	require.Len(t, h.sink.exposures, 2)
}

func TestDependentFlags_PublicNumericReadsRequireLosslessConversion(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	makeFlag := func(key string, flagType valueType, value any) *flag {
		configured := dependencyTestFlag(key, "selected", nil)
		configured.VariationType = flagType
		configured.Variations["selected"].Value = value
		return configured
	}
	h.provider.mu.Lock()
	h.provider.configuration = dependencyTestConfig(
		makeFlag("whole-number", valueTypeNumeric, float64(6)),
		makeFlag("fraction", valueTypeNumeric, 1.5),
		makeFlag("too-large-number", valueTypeNumeric, 1e300),
		makeFlag("exact-integer", valueTypeInteger, int64(5)),
		makeFlag("lossy-integer", valueTypeInteger, int64(9007199254740993)),
	)
	h.provider.mu.Unlock()

	ctx := context.Background()
	evalCtx := of.NewEvaluationContext("subject", nil)

	integer, err := h.client.IntValueDetails(ctx, "whole-number", -1, evalCtx)
	require.NoError(t, err)
	require.Equal(t, int64(6), integer.Value)

	for _, key := range []string{"fraction", "too-large-number"} {
		integer, err = h.client.IntValueDetails(ctx, key, -1, evalCtx)
		require.Error(t, err)
		require.Equal(t, int64(-1), integer.Value)
		require.Equal(t, of.TypeMismatchCode, integer.ErrorCode)
	}

	number, err := h.client.FloatValueDetails(ctx, "exact-integer", -1, evalCtx)
	require.NoError(t, err)
	require.Equal(t, 5.0, number.Value)

	number, err = h.client.FloatValueDetails(ctx, "lossy-integer", -1, evalCtx)
	require.Error(t, err)
	require.Equal(t, -1.0, number.Value)
	require.Equal(t, of.TypeMismatchCode, number.ErrorCode)
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

func TestIndependentEvaluationDoesNotCreateDependencyTrace(t *testing.T) {
	config := dependencyTestConfig(&flag{
		Key: "root", Enabled: true, VariationType: valueTypeBoolean,
		Variations: map[string]*variant{},
	})
	prepareDependencyConditions(config)

	result := evaluateConfiguredFlag(config, "root", false, nil, time.Time{}, experimentationExclusion{})
	require.NoError(t, result.Error)
	require.Empty(t, result.DependencyEvaluations)
	require.Empty(t, dependencyExposureKeys(result))
}

func BenchmarkIndependentEvaluationWithoutDependencyTrace(b *testing.B) {
	config := dependencyTestConfig(&flag{
		Key: "root", Enabled: true, VariationType: valueTypeBoolean,
		Variations: map[string]*variant{},
	})
	prepareDependencyConditions(config)
	b.ReportAllocs()
	for b.Loop() {
		result := evaluateConfiguredFlag(config, "root", false, nil, time.Time{}, experimentationExclusion{})
		if result.Error != nil || len(result.DependencyEvaluations) != 0 {
			b.Fatalf("unexpected independent result: error=%v dependencyEvaluations=%d", result.Error, len(result.DependencyEvaluations))
		}
	}
}

func dependencyTestConfigWithDepth(depth int, flags ...*flag) *universalFlagsConfiguration {
	config := dependencyTestConfig(flags...)
	config.EvaluatorParams = json.RawMessage(fmt.Sprintf(`{"maxDependencyDepth":%d}`, depth))
	return config
}

func dependencyChainConfig(edges int) *universalFlagsConfiguration {
	flags := make([]*flag, 0, edges+1)
	for depth := edges; depth >= 0; depth-- {
		key := "root"
		if depth > 0 {
			key = fmt.Sprintf("dependency-%d", depth)
		}
		if depth == edges {
			flags = append(flags, dependencyTestFlag(key, "on", nil))
			continue
		}
		next := fmt.Sprintf("dependency-%d", depth+1)
		flags = append(flags, dependencyTestFlag(key, "on", dependencyCondition(next, operatorOneOf, "on")))
	}
	return dependencyTestConfig(flags...)
}

func evaluateConfiguredFlagWithCounter(config *universalFlagsConfiguration, flagKey string, onEvaluate func(string)) evaluationResult {
	now := time.Now()
	session := &dependencyEvaluationSession{
		config:            config,
		now:               now,
		maxDepth:          configuredMaxDependencyDepth(config),
		active:            map[string]bool{flagKey: true},
		memo:              make(map[string]memoizedDependency),
		exposureKeys:      make(map[string]struct{}),
		evaluationIndexes: make(map[string]int),
		onEvaluate:        onEvaluate,
	}
	result := evaluateConfiguredFlagOnce(config, flagKey, false, nil, now, experimentationExclusion{},
		dependencyResolverAtDepth{session: session, depth: 1})
	result.DependencyEvaluations = cloneDependencyEvaluationRecords(session.evaluations)
	if result.Error == nil && len(session.exposures) > 0 {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any, 1)
		}
		result.Metadata[metadataDependencyExposuresKey] = encodeDependencyExposureCandidates(session.exposures)
	}
	return result
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
