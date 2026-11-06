// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

type evaluatorTestProvider struct {
	of.NoopProvider
	evaluations chan of.FlattenedContext
}

func (evaluatorTestProvider) Metadata() of.Metadata {
	return of.Metadata{Name: datadogProviderName}
}

func (provider evaluatorTestProvider) ObjectEvaluation(_ context.Context, _ string, _ any, evaluationContext of.FlattenedContext) of.InterfaceResolutionDetail {
	provider.evaluations <- evaluationContext
	return of.InterfaceResolutionDetail{Value: map[string]any{"value": "ok"}}
}

type waitingEvaluatorTestProvider struct {
	evaluatorTestProvider
	started chan<- struct{}
	ready   <-chan struct{}
}

func (provider waitingEvaluatorTestProvider) Init(of.EvaluationContext) error {
	close(provider.started)
	<-provider.ready
	return nil
}

func (waitingEvaluatorTestProvider) Shutdown() {}

func TestRegisteredEvaluatorUsesDefaultDatadogProvider(t *testing.T) {
	of.Shutdown()
	provider := evaluatorTestProvider{evaluations: make(chan of.FlattenedContext, 1)}
	if err := of.SetProviderAndWait(provider); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { of.Shutdown() })

	evaluate, err := internalffe.NewEvaluator("test-domain")
	if err != nil {
		t.Fatal(err)
	}
	value, err := evaluate(context.Background(), "flag", "alice", map[string]any{"tier": "gold"})
	if err != nil || value.(map[string]any)["value"] != "ok" {
		t.Fatalf("value=%#v err=%v", value, err)
	}
	evaluationContext := <-provider.evaluations
	if evaluationContext[of.TargetingKey] != "alice" || evaluationContext["tier"] != "gold" {
		t.Fatalf("evaluation context=%#v", evaluationContext)
	}
}

func TestRegisteredEvaluatorWaitsForProvider(t *testing.T) {
	of.Shutdown()
	started := make(chan struct{})
	ready := make(chan struct{})
	provider := waitingEvaluatorTestProvider{
		evaluatorTestProvider: evaluatorTestProvider{evaluations: make(chan of.FlattenedContext, 1)},
		started:               started,
		ready:                 ready,
	}
	if err := of.SetProvider(provider); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { of.Shutdown() })
	<-started

	evaluate, err := internalffe.NewEvaluator("test-domain")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := evaluate(canceled, "flag", "alice", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
	select {
	case <-provider.evaluations:
		t.Fatal("flag evaluated before provider was ready")
	default:
	}
	close(ready)
	if _, err := evaluate(context.Background(), "flag", "alice", nil); err != nil {
		t.Fatal(err)
	}
}

func TestInternalAnyEvaluatorAcceptsScalarFlag(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{})
	evaluate := newAnyEvaluator(h.client)

	value, err := evaluate(context.Background(), "experiment", "subject", nil)
	require.NoError(t, err)
	require.Equal(t, true, value)
}

func TestEvaluateShard(t *testing.T) {
	t.Run("targeting key hashes to correct shard", func(t *testing.T) {
		targetingKey := "user-123"
		salt := "test-salt"
		totalShards := 8192

		// First compute where this key actually hashes
		actualShard := computeShardIndex(salt, targetingKey, totalShards)

		// Test that a range containing this shard matches
		shard := &shard{
			Salt: salt,
			Ranges: []*shardRange{
				{Start: actualShard, End: actualShard + 1},
			},
			TotalShards: totalShards,
		}
		context := map[string]any{
			"targetingKey": targetingKey,
		}

		result, err := evaluateShard(shard, context)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result {
			t.Errorf("expected shard to match when range includes computed shard %d", actualShard)
		}
	})

	t.Run("targeting key not in range", func(t *testing.T) {
		targetingKey := "user-123"
		salt := "test-salt"
		totalShards := 8192

		// Compute where this key hashes
		actualShard := computeShardIndex(salt, targetingKey, totalShards)

		// Create a range that definitely doesn't include this shard
		excludedStart := (actualShard + 100) % totalShards
		excludedEnd := (actualShard + 110) % totalShards
		if excludedEnd < excludedStart {
			excludedEnd = totalShards
		}

		shard := &shard{
			Salt: salt,
			Ranges: []*shardRange{
				{Start: excludedStart, End: excludedEnd},
			},
			TotalShards: totalShards,
		}
		context := map[string]any{
			"targetingKey": targetingKey,
		}

		result, err := evaluateShard(shard, context)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result {
			t.Errorf("expected shard not to match when range excludes computed shard")
		}
	})

	t.Run("no targeting key", func(t *testing.T) {
		shard := &shard{
			Salt: "test-salt",
			Ranges: []*shardRange{
				{Start: 0, End: 8192},
			},
			TotalShards: 8192,
		}
		context := map[string]any{}

		result, err := evaluateShard(shard, context)
		if result {
			t.Errorf("expected shard not to match when no targeting key present")
		}
		if !errors.Is(err, errTargetingKeyMissing) {
			t.Errorf("expected errTargetingKeyMissing, got %v", err)
		}
	})

	t.Run("full range always matches", func(t *testing.T) {
		shard := &shard{
			Salt: "test-salt",
			Ranges: []*shardRange{
				{Start: 0, End: 8192}, // 100% of traffic
			},
			TotalShards: 8192,
		}
		context := map[string]any{
			"targetingKey": "any-user",
		}

		result, err := evaluateShard(shard, context)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result {
			t.Errorf("expected shard to match when range covers all shards")
		}
	})
}

func TestComputeShardIndex(t *testing.T) {
	// Test consistency: same input should always produce same output
	key1 := computeShardIndex("salt1", "user-123", 8192)
	key2 := computeShardIndex("salt1", "user-123", 8192)
	if key1 != key2 {
		t.Errorf("shard index should be consistent: %d != %d", key1, key2)
	}

	// Test different inputs produce different outputs
	key3 := computeShardIndex("salt2", "user-123", 8192)
	if key1 == key3 {
		t.Logf("warning: different salts produced same shard (possible but unlikely)")
	}

	// Test that output is within bounds
	if key1 < 0 || key1 >= 8192 {
		t.Errorf("shard index out of bounds: %d", key1)
	}
}

func TestEvaluateSemverCondition(t *testing.T) {
	tests := []struct {
		name      string
		operator  conditionOperator
		attribute any
		comparand any
		want      bool
	}{
		{name: "equal", operator: operatorSemverEQ, attribute: "1.2.3", comparand: "1.2.3", want: true},
		{name: "equal mismatch", operator: operatorSemverEQ, attribute: "1.2.4", comparand: "1.2.3"},
		{name: "not equal", operator: operatorSemverNEQ, attribute: "1.2.4", comparand: "1.2.3", want: true},
		{name: "not equal mismatch", operator: operatorSemverNEQ, attribute: "1.2.3", comparand: "1.2.3"},
		{name: "less than", operator: operatorSemverLT, attribute: "1.9.9", comparand: "2.0.0", want: true},
		{name: "less than mismatch", operator: operatorSemverLT, attribute: "2.0.0", comparand: "2.0.0"},
		{name: "less than or equal", operator: operatorSemverLTE, attribute: "2.0.0", comparand: "2.0.0", want: true},
		{name: "less than or equal mismatch", operator: operatorSemverLTE, attribute: "2.0.1", comparand: "2.0.0"},
		{name: "greater than", operator: operatorSemverGT, attribute: "1.0.1", comparand: "1.0.0", want: true},
		{name: "greater than mismatch", operator: operatorSemverGT, attribute: "1.0.0", comparand: "1.0.0"},
		{name: "greater than or equal", operator: operatorSemverGTE, attribute: "1.0.0", comparand: "1.0.0", want: true},
		{name: "greater than or equal mismatch", operator: operatorSemverGTE, attribute: "0.9.9", comparand: "1.0.0"},
		{name: "prerelease before release", operator: operatorSemverLT, attribute: "1.0.0-beta.1", comparand: "1.0.0", want: true},
		{name: "numeric prerelease ordering", operator: operatorSemverLT, attribute: "1.0.0-beta.2", comparand: "1.0.0-beta.11", want: true},
		{name: "equal ignores build metadata", operator: operatorSemverEQ, attribute: "4.0.0+build.42", comparand: "4.0.0", want: true},
		{name: "equal ignores dotted build metadata", operator: operatorSemverEQ, attribute: "4.0.0+exp.sha.5114f85", comparand: "4.0.0", want: true},
		{name: "not equal ignores build metadata", operator: operatorSemverNEQ, attribute: "4.0.0+build.42", comparand: "4.0.0"},
		{name: "less than ignores build metadata", operator: operatorSemverLT, attribute: "4.0.0+build.42", comparand: "4.0.0"},
		{name: "less than or equal ignores build metadata", operator: operatorSemverLTE, attribute: "4.0.0+build.42", comparand: "4.0.0", want: true},
		{name: "greater than ignores build metadata", operator: operatorSemverGT, attribute: "4.0.0+build.42", comparand: "4.0.0"},
		{name: "greater than or equal ignores build metadata", operator: operatorSemverGTE, attribute: "4.0.0+build.42", comparand: "4.0.0", want: true},
		{name: "different build metadata has equal precedence", operator: operatorSemverEQ, attribute: "1.0.0+linux", comparand: "1.0.0+darwin", want: true},
		{name: "invalid attribute", operator: operatorSemverNEQ, attribute: "not-a-version", comparand: "1.0.0"},
		{name: "two-part attribute", operator: operatorSemverGTE, attribute: "1.2", comparand: "1.0.0", want: true},
		{name: "prefixed attribute", operator: operatorSemverGTE, attribute: "v1.2.3", comparand: "1.0.0"},
		{name: "overflowing attribute", operator: operatorSemverGTE, attribute: "18446744073709551616.0.0", comparand: "1.0.0"},
		{name: "non-string attribute", operator: operatorSemverEQ, attribute: 1.2, comparand: "1.2.0"},
		{name: "invalid comparand", operator: operatorSemverNEQ, attribute: "1.2.3", comparand: "not-a-version"},
		{name: "non-string comparand", operator: operatorSemverEQ, attribute: "1.2.3", comparand: 1.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparand, _ := tt.comparand.(string)
			var parsedComparand *parsedSemver
			if parsed, ok := parseSemver(comparand); ok {
				parsedComparand = &parsed
			}
			condition := &condition{
				Operator:        tt.operator,
				Attribute:       "version",
				Value:           tt.comparand,
				semverComparand: parsedComparand,
			}
			context := map[string]any{"version": tt.attribute}
			if got := evaluateCondition(condition, context); got != tt.want {
				t.Errorf("evaluateCondition() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("missing attribute", func(t *testing.T) {
		comparand, ok := parseSemver("1.2.3")
		if !ok {
			t.Fatal("parseSemver failed")
		}
		condition := &condition{
			Operator:        operatorSemverEQ,
			Attribute:       "version",
			Value:           "1.2.3",
			semverComparand: &comparand,
		}
		if evaluateCondition(condition, map[string]any{}) {
			t.Error("expected a missing attribute not to match")
		}
	})

	t.Run("unsupported operator", func(t *testing.T) {
		comparand, ok := parseSemver("1.2.3")
		if !ok {
			t.Fatal("parseSemver failed")
		}
		if evaluateSemverCondition("1.2.3", &comparand, conditionOperator("UNKNOWN")) {
			t.Error("expected an unsupported operator not to match")
		}
	})
}

func TestValidateVariantType(t *testing.T) {
	tests := []struct {
		name         string
		value        any
		expectedType valueType
		expectError  bool
	}{
		{"boolean valid", true, valueTypeBoolean, false},
		{"boolean invalid", "true", valueTypeBoolean, true},
		{"string valid", "hello", valueTypeString, false},
		{"string invalid", 123, valueTypeString, true},
		{"integer valid int", 42, valueTypeInteger, false},
		{"integer valid int64", int64(42), valueTypeInteger, false},
		{"integer valid float64 whole", float64(42), valueTypeInteger, false},
		{"integer invalid float64 decimal", 42.5, valueTypeInteger, true},
		{"numeric valid int", 42, valueTypeNumeric, false},
		{"numeric valid float", 42.5, valueTypeNumeric, false},
		{"numeric invalid", "42", valueTypeNumeric, true},
		{"json accepts anything", map[string]any{"key": "value"}, valueTypeJSON, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVariantType(tt.value, tt.expectedType)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}
		})
	}
}

func TestEvaluateFlag_VariantTypeMismatchReturnsParseError(t *testing.T) {
	// When the configuration declares a flag type (e.g., INTEGER) but the variant
	// value doesn't match (e.g., a string), we should return errParseError so that
	// toResolutionError maps it to PARSE_ERROR.
	tests := []struct {
		name          string
		variationType valueType
		variantValue  any
	}{
		{
			name:          "INTEGER flag with string value",
			variationType: valueTypeInteger,
			variantValue:  "not-an-integer",
		},
		{
			name:          "BOOLEAN flag with string value",
			variationType: valueTypeBoolean,
			variantValue:  "true",
		},
		{
			name:          "NUMERIC flag with string value",
			variationType: valueTypeNumeric,
			variantValue:  "42.5",
		},
		{
			name:          "STRING flag with integer value",
			variationType: valueTypeString,
			variantValue:  123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := &flag{
				Key:           "test-flag",
				Enabled:       true,
				VariationType: tt.variationType,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: tt.variantValue},
				},
				Allocations: []*allocation{
					{
						Key: "allocation1",
						Splits: []*split{
							{
								VariationKey: "v1",
							},
						},
					},
				},
			}

			result := evaluateFlag(flag, nil, map[string]any{"targetingKey": "user-123"}, time.Now(), experimentationExclusion{})

			if result.Reason != of.ErrorReason {
				t.Errorf("expected ErrorReason, got %s", result.Reason)
			}
			if result.Error == nil {
				t.Fatal("expected error, got nil")
			}
			// Verify the error wraps errParseError so toResolutionError maps to PARSE_ERROR
			if !errors.Is(result.Error, errParseError) {
				t.Errorf("expected error to wrap errParseError, got: %v", result.Error)
			}
		})
	}
}

type fixtureEvaluationResult struct {
	Value     any                       `json:"value"`
	Reason    string                    `json:"reason"`
	ErrorCode string                    `json:"errorCode"`
	SerialID  json.RawMessage           `json:"serialId"`
	DoLog     *bool                     `json:"doLog"`
	Features  map[string]map[string]any `json:"features"`
}

type fixtureEvaluationCase struct {
	Description   string                             `json:"description"`
	Flag          string                             `json:"flag"`
	DefaultValue  any                                `json:"defaultValue"`
	TargetingKey  *string                            `json:"targetingKey"`
	Attributes    map[string]any                     `json:"attributes"`
	VariationType string                             `json:"variationType"`
	Result        fixtureEvaluationResult            `json:"result"`
	Capabilities  map[string]fixtureEvaluationResult `json:"capabilities"`
	Expectations  fixtureExpectations                `json:"expectations"`
}

type fixtureExpectations struct {
	Exposures         []fixtureExpectedExposure        `json:"exposures"`
	EvaluationEvents  []fixtureExpectedEvaluationEvent `json:"evaluationEvents"`
	NoUnmatchedEvents bool                             `json:"noUnmatchedEvents"`
}

type fixtureExpectedExposure struct {
	Count      int    `json:"_count"`
	Allocation string `json:"allocation"`
	Flag       string `json:"flag"`
	Variant    string `json:"variant"`
}

type fixtureExpectedEvaluationEvent struct {
	Count     int     `json:"_count"`
	Flag      string  `json:"flag"`
	ErrorCode *string `json:"errorCode"`
}

type fixtureCaseFile struct {
	name  string
	cases []fixtureEvaluationCase
}

type fixtureSuite struct {
	name              string
	configurationPath string
	casesPath         string
	assertTelemetry   bool
}

func loadFixtureSuite(t *testing.T, suite fixtureSuite) ([]fixtureCaseFile, []byte, int) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(suite.casesPath, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no canonical FFE evaluation-case fixtures found (initialize them with `git submodule update --init --recursive`)")
	}

	fixtureFiles := make([]fixtureCaseFile, 0, len(files))
	caseCount := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var cases []fixtureEvaluationCase
		if err := json.Unmarshal(data, &cases); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		caseCount += len(cases)
		fixtureFiles = append(fixtureFiles, fixtureCaseFile{name: filepath.Base(file), cases: cases})
	}
	if caseCount == 0 {
		t.Fatal("no canonical FFE evaluation cases found")
	}
	configData, err := os.ReadFile(suite.configurationPath)
	if err != nil {
		t.Fatalf("read canonical FFE fixtures: %v (initialize them with `git submodule update --init --recursive`)", err)
	}
	return fixtureFiles, configData, caseCount
}

func fixtureRevision(fixtureDir string) (string, error) {
	out, err := exec.Command("git", "-C", fixtureDir, "rev-parse", "--show-toplevel", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolve fixture revision: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", fmt.Errorf("resolve fixture revision: expected repository root and revision, got %d lines", len(lines))
	}
	fixtureInfo, err := os.Stat(fixtureDir)
	if err != nil {
		return "", fmt.Errorf("resolve fixture revision: stat fixture directory: %w", err)
	}
	repositoryRoot := strings.TrimSpace(lines[0])
	repositoryInfo, err := os.Stat(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("resolve fixture revision: stat repository root: %w", err)
	}
	if !os.SameFile(fixtureInfo, repositoryInfo) {
		return "", fmt.Errorf("resolve fixture revision: fixture directory %q is not repository root %q", fixtureDir, repositoryRoot)
	}
	revision := strings.TrimSpace(lines[1])
	if revision == "" {
		return "", errors.New("resolve fixture revision: empty revision")
	}
	return revision, nil
}

func TestFixtureRevisionRejectsMissingGitMetadata(t *testing.T) {
	_, err := fixtureRevision(t.TempDir())
	require.Error(t, err)
}

func TestFixtureRevisionRejectsParentGitMetadata(t *testing.T) {
	// The package directory is inside the superproject but is not itself a Git root.
	_, err := fixtureRevision(".")
	require.Error(t, err)
}

func TestEvaluateFlag_JSONFixtures(t *testing.T) {
	fixtureRoot := "ffe-system-test-data"
	revision, err := fixtureRevision(fixtureRoot)
	require.NoError(t, err)
	for _, suite := range []fixtureSuite{
		{
			name:              "Core",
			configurationPath: filepath.Join(fixtureRoot, "ufc-config.json"),
			casesPath:         filepath.Join(fixtureRoot, "evaluation-cases"),
		},
		{
			name:              "Dependent",
			configurationPath: filepath.Join(fixtureRoot, "evaluation-suites", "dependent-flags", "ufc-config.json"),
			casesPath:         filepath.Join(fixtureRoot, "evaluation-suites", "dependent-flags", "cases"),
			assertTelemetry:   true,
		},
	} {
		t.Run(suite.name, func(t *testing.T) {
			runEvaluateFlagJSONFixtures(t, suite, revision)
		})
	}
}

func runEvaluateFlagJSONFixtures(t *testing.T, suite fixtureSuite, revision string) {
	t.Helper()
	fixtureFiles, configData, caseCount := loadFixtureSuite(t, suite)
	t.Logf("canonical FFE fixtures suite=%s revision=%s cases=%d", suite.name, revision, caseCount)

	var cfg universalFlagsConfiguration
	if err := json.Unmarshal(configData, &cfg); err != nil {
		t.Fatal(err)
	}

	provider := newDatadogProvider(ProviderConfig{})
	provider.updateConfiguration(&cfg)
	if err := of.SetProviderAndWait(provider); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	immediateProvider := newDatadogProvider(ProviderConfig{
		ExperimentationOptOutMode: ExperimentationOptOutImmediate,
	})
	t.Cleanup(immediateProvider.Shutdown)
	// Reuse the configuration without starting telemetry workers for capability checks.
	immediateProvider.configuration = &cfg
	futureProvider := newDatadogProvider(ProviderConfig{
		ExperimentationOptOutMode: ExperimentationOptOutFutureExperimentsOnly,
	})
	t.Cleanup(futureProvider.Shutdown)
	futureProvider.configuration = &cfg
	capabilityProviders := map[string]*DatadogProvider{
		"experimentationOptOutImmediate":             immediateProvider,
		"experimentationOptOutFutureExperimentsOnly": futureProvider,
	}
	capabilityCases := make(map[string]int)
	// Pin each supported semantic family independently; additional cases are welcome.
	// A global capability count can hide a missing family behind another file.
	requiredFamilies := map[string]map[string]int{
		"test-case-experimentation-opt-out.json": {
			"experimentationOptOutImmediate":             26,
			"experimentationOptOutFutureExperimentsOnly": 26,
		},
		"test-case-experimentation-opt-out-future.json": {
			"experimentationOptOutFutureExperimentsOnly": 16,
		},
	}
	familyCases := make(map[string]map[string]int)

	for _, file := range fixtureFiles {
		// Count discovered cases, not selected subtests, so -run filters remain usable.
		counts := make(map[string]int)
		familyCases[file.name] = counts
		for _, tc := range file.cases {
			for capability := range tc.Capabilities {
				capabilityCases[capability]++
				counts[capability]++
			}
		}
		t.Run(file.name, func(t *testing.T) {
			for i, tc := range file.cases {
				tkLabel := "<nil>"
				if tc.TargetingKey != nil {
					tkLabel = *tc.TargetingKey
				}
				t.Run(fmt.Sprintf("case%d/%s", i, tkLabel), func(t *testing.T) {
					if suite.assertTelemetry {
						assertDependentFixtureCase(t, configData, tc)
						return
					}
					ctx := make(map[string]any, len(tc.Attributes)+1)
					maps.Copy(ctx, tc.Attributes)
					if tc.TargetingKey != nil {
						ctx["targetingKey"] = *tc.TargetingKey
					}

					result := evaluateConfiguredFlag(&cfg, tc.Flag, tc.DefaultValue, ctx, time.Now(), experimentationExclusion{})

					if fmt.Sprintf("%v", result.Value) != fmt.Sprintf("%v", tc.Result.Value) {
						t.Errorf("value: got %v, want %v", result.Value, tc.Result.Value)
					}
					if tc.Result.Reason != "" && result.Reason != of.Reason(tc.Result.Reason) {
						t.Errorf("reason: got %q, want %q", result.Reason, tc.Result.Reason)
					}
					if tc.Result.SerialID != nil {
						// Omission is unspecified; JSON null asserts absence, unlike zero.
						var expected *uint32
						require.NoError(t, json.Unmarshal(tc.Result.SerialID, &expected))
						actual, present := result.Metadata[metadataSplitSerialIDKey]
						assert.Equal(t, expected != nil, present, "serial ID presence")
						if expected != nil {
							assert.Equal(t, *expected, actual, "serial ID")
						}
					}
					if tc.Result.DoLog != nil {
						assert.Equal(t, *tc.Result.DoLog, result.Metadata[metadataDoLogKey], "doLog")
					}
					if tc.Result.Features != nil {
						assertFixtureFeatures(t, &cfg, tc, result)
					}
					if tc.Result.ErrorCode != "" {
						result := provider.evaluateAny(context.Background(), tc.Flag, tc.DefaultValue, ctx)
						if result.Error == nil {
							t.Error("expected SDK evaluation error, got nil")
						}
						if code := errorCodeFor(result.Error); code != of.ErrorCode(tc.Result.ErrorCode) {
							t.Errorf("error code: got %q, want %q", code, tc.Result.ErrorCode)
						}
					}
					for capability, expected := range tc.Capabilities {
						p, supported := capabilityProviders[capability]
						if !supported {
							continue
						}
						t.Run(capability, func(t *testing.T) {
							result := p.evaluateAny(context.Background(), tc.Flag, tc.DefaultValue, ctx)
							assert.Equal(t, expected.Value, result.Value, "capability result value")
							if result.Reason != of.Reason(expected.Reason) {
								t.Errorf("reason: got %q, want %q", result.Reason, expected.Reason)
							}
							if code := errorCodeFor(result.Error); code != of.ErrorCode(expected.ErrorCode) {
								t.Errorf("error code: got %q, want %q", code, expected.ErrorCode)
							}
						})
					}
				})
			}
		})
	}
	if !suite.assertTelemetry {
		for family, minimums := range requiredFamilies {
			for capability, minimum := range minimums {
				if count := familyCases[family][capability]; count < minimum {
					t.Errorf("canonical fixture family %q has %d cases for capability %q; require at least %d", family, count, capability, minimum)
				}
			}
		}
		for capability := range capabilityProviders {
			if capabilityCases[capability] == 0 {
				t.Errorf("no canonical fixture cases found for supported capability %q", capability)
			}
		}
	}
	t.Logf("canonical FFE fixture capabilities=%v", capabilityCases)
}

type fixtureResolution struct {
	value     any
	reason    of.Reason
	errorCode of.ErrorCode
}

func assertDependentFixtureCase(t *testing.T, configData []byte, tc fixtureEvaluationCase) {
	t.Helper()
	var cfg universalFlagsConfiguration
	require.NoError(t, json.Unmarshal(configData, &cfg))
	prepareDependencyConditions(&cfg)

	// Each corpus case gets an isolated provider, cache, writers, and transport
	// sink so no earlier subject or aggregation bucket can affect its events.
	h := newOptOutHarness(t, ProviderConfig{})
	h.provider.mu.Lock()
	h.provider.configuration = &cfg
	h.provider.mu.Unlock()

	resolution := evaluateFixtureThroughClient(t, h.client, tc)
	assert.Equal(t, fmt.Sprint(tc.Result.Value), fmt.Sprint(resolution.value), "result value")
	if tc.Result.Reason != "" {
		assert.Equal(t, of.Reason(tc.Result.Reason), resolution.reason, "result reason")
	}
	if tc.Result.ErrorCode != "" {
		assert.Equal(t, of.ErrorCode(tc.Result.ErrorCode), resolution.errorCode, "result error code")
	}

	h.provider.flagEvalLoggingWriter.drainAndFlush()
	h.provider.exposureWriter.flush()
	if tc.Expectations.Exposures != nil {
		assertFixtureExpectedExposures(t, tc.Expectations, h.sink.exposures)
	}
	if tc.Expectations.EvaluationEvents != nil {
		assertFixtureExpectedEvaluationEvents(t, tc.Expectations, h.sink.events)
	}
}

func evaluateFixtureThroughClient(t *testing.T, client *of.Client, tc fixtureEvaluationCase) fixtureResolution {
	t.Helper()
	targetingKey := ""
	if tc.TargetingKey != nil {
		targetingKey = *tc.TargetingKey
	}
	evaluationContext := of.NewEvaluationContext(targetingKey, tc.Attributes)
	ctx := context.Background()
	switch tc.VariationType {
	case "BOOLEAN":
		defaultValue, _ := tc.DefaultValue.(bool)
		details, _ := client.BooleanValueDetails(ctx, tc.Flag, defaultValue, evaluationContext)
		return fixtureResolution{value: details.Value, reason: details.Reason, errorCode: details.ErrorCode}
	case "STRING":
		defaultValue, _ := tc.DefaultValue.(string)
		details, _ := client.StringValueDetails(ctx, tc.Flag, defaultValue, evaluationContext)
		return fixtureResolution{value: details.Value, reason: details.Reason, errorCode: details.ErrorCode}
	case "INTEGER":
		defaultValue := fixtureInteger(tc.DefaultValue)
		details, _ := client.IntValueDetails(ctx, tc.Flag, defaultValue, evaluationContext)
		return fixtureResolution{value: details.Value, reason: details.Reason, errorCode: details.ErrorCode}
	case "NUMERIC":
		defaultValue := fixtureFloat(tc.DefaultValue)
		details, _ := client.FloatValueDetails(ctx, tc.Flag, defaultValue, evaluationContext)
		return fixtureResolution{value: details.Value, reason: details.Reason, errorCode: details.ErrorCode}
	case "JSON":
		details, _ := client.ObjectValueDetails(ctx, tc.Flag, tc.DefaultValue, evaluationContext)
		return fixtureResolution{value: details.Value, reason: details.Reason, errorCode: details.ErrorCode}
	default:
		t.Fatalf("unsupported fixture variationType %q", tc.VariationType)
		return fixtureResolution{}
	}
}

func fixtureInteger(value any) int64 {
	switch value := value.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func fixtureFloat(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case int64:
		return float64(value)
	case int:
		return float64(value)
	default:
		return 0
	}
}

func fixtureExpectedCount(count int) int {
	if count <= 0 {
		return 1
	}
	return count
}

func assertFixtureExpectedExposures(t *testing.T, expectations fixtureExpectations, actual []exposureEvent) {
	t.Helper()
	remaining := append([]exposureEvent(nil), actual...)
	for _, expected := range expectations.Exposures {
		for range fixtureExpectedCount(expected.Count) {
			match := -1
			for i, event := range remaining {
				if event.Flag.Key != expected.Flag ||
					(expected.Allocation != "" && event.Allocation.Key != expected.Allocation) ||
					(expected.Variant != "" && event.Variant.Key != expected.Variant) {
					continue
				}
				match = i
				break
			}
			require.NotEqual(t, -1, match, "missing expected exposure %+v; actual=%+v", expected, actual)
			remaining = append(remaining[:match], remaining[match+1:]...)
		}
	}
	if expectations.NoUnmatchedEvents {
		assert.Empty(t, remaining, "unexpected exposures")
	}
}

func assertFixtureExpectedEvaluationEvents(t *testing.T, expectations fixtureExpectations, actual []flagEvalLoggingEvent) {
	t.Helper()
	remaining := make([]flagEvalLoggingEvent, 0, len(actual))
	for _, event := range actual {
		for range max(int(event.EvaluationCount), 1) {
			remaining = append(remaining, event)
		}
	}
	for _, expected := range expectations.EvaluationEvents {
		for range fixtureExpectedCount(expected.Count) {
			match := -1
			for i, event := range remaining {
				actualErrorCode := ""
				if event.Error != nil {
					actualErrorCode = event.Error.Message
				}
				expectedErrorCode := ""
				if expected.ErrorCode != nil {
					expectedErrorCode = *expected.ErrorCode
				}
				if event.Flag.Key == expected.Flag && actualErrorCode == expectedErrorCode {
					match = i
					break
				}
			}
			require.NotEqual(t, -1, match, "missing expected evaluation event %+v; actual=%+v", expected, actual)
			remaining = append(remaining[:match], remaining[match+1:]...)
		}
	}
	if expectations.NoUnmatchedEvents {
		assert.Empty(t, remaining, "unexpected evaluation events")
	}
}

// assertFixtureFeatures runs every case declaring result.features through the
// real client hooks and synchronous writers. Expectations come only from the
// shared fixtures, including empty maps and features selected with doLog=false.
func assertFixtureFeatures(t *testing.T, cfg *universalFlagsConfiguration, tc fixtureEvaluationCase, selected evaluationResult) {
	t.Helper()
	assertFeatures := func(destination string, actual map[string]any) {
		t.Helper()
		expected, present := tc.Result.Features[destination]
		require.True(t, present, "missing %s feature expectation", destination)
		if actual == nil {
			actual = map[string]any{}
		}
		assert.Equal(t, expected, actual, "%s features", destination)
	}
	assertFeatures("EXPOSURE", decodedFeatureValues(selected.Metadata, metadataExposureFeaturesKey))
	assertFeatures("EVALUATION", decodedFeatureValues(selected.Metadata, metadataEvaluationFeaturesKey))

	// A fresh cache per case prevents identical subjects in independent fixtures
	// from suppressing delivery. The sink decodes actual serialized EVP payloads.
	h := newOptOutHarness(t, ProviderConfig{})
	// As in newOptOutHarness, install without starting background flush workers.
	h.provider.mu.Lock()
	h.provider.configuration = cfg
	h.provider.mu.Unlock()
	var callbacks []ExposureEvaluation
	h.client.AddHooks(NewExposureHook(func(e ExposureEvaluation) { callbacks = append(callbacks, e) }))
	targetingKey := ""
	if tc.TargetingKey != nil {
		targetingKey = *tc.TargetingKey
	}
	evaluate := newAnyEvaluator(h.client)
	value, err := evaluate(context.Background(), tc.Flag, targetingKey, tc.Attributes)
	require.NoError(t, err)
	assert.Equal(t, tc.Result.Value, value)
	require.Len(t, callbacks, 1)
	assertFeatures("HOOK", callbacks[0].Features())
	if tc.Result.DoLog != nil {
		assert.Equal(t, *tc.Result.DoLog, callbacks[0].ShouldSend())
	}
	assert.False(t, callbacks[0].IsCacheHit())

	h.provider.exposureWriter.flush()
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	if tc.Result.DoLog != nil {
		if *tc.Result.DoLog {
			require.Len(t, h.sink.exposures, 1)
		} else {
			assert.Empty(t, h.sink.exposures, "doLog=false suppresses exposure delivery, not selected features")
		}
	}
	for _, exposure := range h.sink.exposures {
		assert.Equal(t, tc.Flag, exposure.Flag.Key)
		assertFeatures("EXPOSURE", exposure.Features)
	}
	require.Len(t, h.sink.events, 1)
	assert.Equal(t, tc.Flag, h.sink.events[0].Flag.Key)
	assert.Equal(t, int64(1), h.sink.events[0].EvaluationCount)
	assertFeatures("EVALUATION", h.sink.events[0].Features)
}
