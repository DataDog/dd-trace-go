// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	ciVisibilityDefaultFlakyRetryCount      = 5
	ciVisibilityDefaultTotalFlakyRetryCount = 1_000
	ciVisibilityMaxCoverageReportFlags      = 32
)

var ciVisibilityFeatureBinding = ConsumerBinding{
	ID:       "civisibility.features",
	Consumer: "internal/civisibility/integrations feature initialization",
	Keys: []string{
		"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED",
		"DD_CIVISIBILITY_FLAKY_RETRY_COUNT",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED",
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED",
		"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED",
		"DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED",
		"DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT",
		"DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES",
		"DD_TEST_MANAGEMENT_ENABLED",
	},
	Sampling:        SampleFirstUse,
	EnvironmentOnly: true,
}

var ciVisibilityEnabledBinding = ConsumerBinding{
	ID:              "civisibility.enabled",
	Consumer:        "CI Visibility bootstrap and Go testing instrumentation",
	Keys:            []string{"DD_CIVISIBILITY_ENABLED"},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

var ciVisibilityBootstrapDebugBinding = ConsumerBinding{
	ID:       "civisibility.bootstrap-debug",
	Consumer: "internal/civisibility/integrations initialization",
	Keys:     []string{"DD_TRACE_DEBUG"},
	Sampling: SampleFirstUse,
}

var ciVisibilityBootstrapServiceBinding = ConsumerBinding{
	ID:              "civisibility.bootstrap-service",
	Consumer:        "internal/civisibility/integrations initialization",
	Keys:            []string{"DD_SERVICE"},
	Sampling:        SampleFirstUse,
	EnvironmentOnly: true,
}

var ciVisibilityLogsBinding = ConsumerBinding{
	ID:       "civisibility.logs",
	Consumer: "internal/civisibility/integrations/logs",
	Keys:     []string{"DD_CIVISIBILITY_LOGS_ENABLED"},
	Sampling: SampleFirstUse,
}

var ciVisibilityClientBinding = ConsumerBinding{
	ID:       "civisibility.client",
	Consumer: "internal/civisibility/utils/net client constructor",
	Keys: []string{
		"DD_API_KEY",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED",
		"DD_CIVISIBILITY_AGENTLESS_URL",
		"DD_ENV",
		"DD_SERVICE",
		"DD_SITE",
		"DD_TAGS",
	},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

var ciVisibilityTelemetryVersionBinding = ConsumerBinding{
	ID:              "civisibility.telemetry-version",
	Consumer:        "CI Visibility telemetry client first use",
	Keys:            []string{"DD_VERSION"},
	Sampling:        SampleFirstUse,
	EnvironmentOnly: true,
}

var ciVisibilityTagBinding = ConsumerBinding{
	ID:       "civisibility.tags",
	Consumer: "internal/civisibility/utils CI tag initialization",
	Keys: []string{
		"DD_ACTION_EXECUTION_ID",
		"DD_CUSTOM_PARENT_ID",
		"DD_CUSTOM_TRACE_ID",
		"DD_GIT_BRANCH",
		"DD_GIT_COMMIT_AUTHOR_DATE",
		"DD_GIT_COMMIT_AUTHOR_EMAIL",
		"DD_GIT_COMMIT_AUTHOR_NAME",
		"DD_GIT_COMMIT_COMMITTER_DATE",
		"DD_GIT_COMMIT_COMMITTER_EMAIL",
		"DD_GIT_COMMIT_COMMITTER_NAME",
		"DD_GIT_COMMIT_MESSAGE",
		"DD_GIT_COMMIT_SHA",
		"DD_GIT_PULL_REQUEST_BASE_BRANCH",
		"DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA",
		"DD_GIT_REPOSITORY_URL",
		"DD_GIT_TAG",
		"DD_PIPELINE_EXECUTION_ID",
		"DD_SERVICE",
		"DD_TEST_SESSION_NAME",
	},
	Sampling:        SampleFirstUse,
	EnvironmentOnly: true,
}

var ciVisibilityEnvironmentDataBinding = ConsumerBinding{
	ID:              "civisibility.environment-data",
	Consumer:        "internal/civisibility/utils environmental data loader",
	Keys:            []string{"DD_TEST_OPTIMIZATION_ENV_DATA_FILE"},
	Sampling:        SamplePerCall,
	EnvironmentOnly: true,
}

var ciVisibilityCoverageFlagsBinding = ConsumerBinding{
	ID:              "civisibility.coverage-flags",
	Consumer:        "internal/civisibility/utils/net coverage report client",
	Keys:            []string{"DD_CODE_COVERAGE_FLAGS"},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

var ciVisibilityAutoInstrumentationBinding = ConsumerBinding{
	ID:              "civisibility.auto-instrumentation",
	Consumer:        "internal/civisibility/utils/telemetry test session metric",
	Keys:            []string{"DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER"},
	Sampling:        SamplePerCall,
	EnvironmentOnly: true,
}

var ciVisibilityParallelEFDBinding = ConsumerBinding{
	ID:              "civisibility.parallel-efd",
	Consumer:        "internal/civisibility/integrations/gotesting retry execution",
	Keys:            []string{"DD_CIVISIBILITY_INTERNAL_PARALLEL_EARLY_FLAKE_DETECTION_ENABLED"},
	Sampling:        SamplePerCall,
	EnvironmentOnly: true,
}

var ciVisibilityTestManagementInstrumentationBinding = ConsumerBinding{
	ID:              "civisibility.test-management-instrumentation",
	Consumer:        "internal/civisibility/integrations/gotesting session initialization",
	Keys:            []string{"DD_TEST_MANAGEMENT_ENABLED"},
	Sampling:        SampleConstructor,
	EnvironmentOnly: true,
}

var ciVisibilityTestOptimizationBinding = ConsumerBinding{
	ID:       "civisibility.test-optimization-mode",
	Consumer: "internal/bazel mode initialization",
	Keys: []string{
		"DD_TEST_OPTIMIZATION_MANIFEST_FILE",
		"DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES",
	},
	Sampling:        SampleFirstUse,
	EnvironmentOnly: true,
}

type ciVisibilitySnapshotState struct {
	once     sync.Once
	reported atomic.Bool
	value    CIVisibilityConfig
	events   []ConfigEvent
}

var ciVisibilitySnapshotStatePointer atomic.Pointer[ciVisibilitySnapshotState]

func init() {
	registerRaw(RawDefinition{Key: "DD_ACTION_EXECUTION_ID", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_FLAKY_RETRY_COUNT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_INTERNAL_PARALLEL_EARLY_FLAKE_DETECTION_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_LOGS_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_CODE_COVERAGE_FLAGS", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_CUSTOM_PARENT_ID", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_CUSTOM_TRACE_ID", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_BRANCH", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_AUTHOR_DATE", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_AUTHOR_EMAIL", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_AUTHOR_NAME", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_COMMITTER_DATE", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_COMMITTER_EMAIL", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_COMMITTER_NAME", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_MESSAGE", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_PULL_REQUEST_BASE_BRANCH", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_GIT_TAG", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_PIPELINE_EXECUTION_ID", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TEST_MANAGEMENT_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TEST_OPTIMIZATION_ENV_DATA_FILE", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TEST_OPTIMIZATION_MANIFEST_FILE", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TEST_SESSION_NAME", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerBinding(ConsumerBinding{ID: "civisibility.features", Consumer: "internal/civisibility/integrations feature initialization", Keys: []string{
		"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED",
		"DD_CIVISIBILITY_FLAKY_RETRY_COUNT",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED",
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED",
		"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED",
		"DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED",
		"DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT",
		"DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES",
		"DD_TEST_MANAGEMENT_ENABLED",
	}, Sampling: SampleFirstUse, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.enabled", Consumer: "CI Visibility bootstrap and Go testing instrumentation", Keys: []string{"DD_CIVISIBILITY_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.bootstrap-debug", Consumer: "internal/civisibility/integrations initialization", Keys: []string{"DD_TRACE_DEBUG"}, Sampling: SampleFirstUse})
	registerBinding(ConsumerBinding{ID: "civisibility.bootstrap-service", Consumer: "internal/civisibility/integrations initialization", Keys: []string{"DD_SERVICE"}, Sampling: SampleFirstUse, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.logs", Consumer: "internal/civisibility/integrations/logs", Keys: []string{"DD_CIVISIBILITY_LOGS_ENABLED"}, Sampling: SampleFirstUse})
	registerBinding(ConsumerBinding{ID: "civisibility.client", Consumer: "internal/civisibility/utils/net client constructor", Keys: []string{
		"DD_API_KEY",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED",
		"DD_CIVISIBILITY_AGENTLESS_URL",
		"DD_ENV",
		"DD_SERVICE",
		"DD_SITE",
		"DD_TAGS",
	}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.telemetry-version", Consumer: "CI Visibility telemetry client first use", Keys: []string{"DD_VERSION"}, Sampling: SampleFirstUse, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.tags", Consumer: "internal/civisibility/utils CI tag initialization", Keys: []string{
		"DD_ACTION_EXECUTION_ID",
		"DD_CUSTOM_PARENT_ID",
		"DD_CUSTOM_TRACE_ID",
		"DD_GIT_BRANCH",
		"DD_GIT_COMMIT_AUTHOR_DATE",
		"DD_GIT_COMMIT_AUTHOR_EMAIL",
		"DD_GIT_COMMIT_AUTHOR_NAME",
		"DD_GIT_COMMIT_COMMITTER_DATE",
		"DD_GIT_COMMIT_COMMITTER_EMAIL",
		"DD_GIT_COMMIT_COMMITTER_NAME",
		"DD_GIT_COMMIT_MESSAGE",
		"DD_GIT_COMMIT_SHA",
		"DD_GIT_PULL_REQUEST_BASE_BRANCH",
		"DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA",
		"DD_GIT_REPOSITORY_URL",
		"DD_GIT_TAG",
		"DD_PIPELINE_EXECUTION_ID",
		"DD_SERVICE",
		"DD_TEST_SESSION_NAME",
	}, Sampling: SampleFirstUse, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.environment-data", Consumer: "internal/civisibility/utils environmental data loader", Keys: []string{"DD_TEST_OPTIMIZATION_ENV_DATA_FILE"}, Sampling: SamplePerCall, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.coverage-flags", Consumer: "internal/civisibility/utils/net coverage report client", Keys: []string{"DD_CODE_COVERAGE_FLAGS"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.auto-instrumentation", Consumer: "internal/civisibility/utils/telemetry test session metric", Keys: []string{"DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER"}, Sampling: SamplePerCall, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.parallel-efd", Consumer: "internal/civisibility/integrations/gotesting retry execution", Keys: []string{"DD_CIVISIBILITY_INTERNAL_PARALLEL_EARLY_FLAKE_DETECTION_ENABLED"}, Sampling: SamplePerCall, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.test-management-instrumentation", Consumer: "internal/civisibility/integrations/gotesting session initialization", Keys: []string{"DD_TEST_MANAGEMENT_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "civisibility.test-optimization-mode", Consumer: "internal/bazel mode initialization", Keys: []string{
		"DD_TEST_OPTIMIZATION_MANIFEST_FILE",
		"DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES",
	}, Sampling: SampleFirstUse, EnvironmentOnly: true})

	ciVisibilitySnapshotStatePointer.Store(new(ciVisibilitySnapshotState))
}

// CIVisibilityConfig contains the feature controls sampled on first use by CI
// Visibility settings and additional-feature initialization.
type CIVisibilityConfig struct {
	GitUploadEnabled                  bool
	FlakyRetryEnabled                 bool
	ImpactedTestsEnabled              bool
	CodeCoverageReportUploadEnabled   bool
	TestManagementEnabled             bool
	TestManagementAttemptToFixRetries int
	SubtestFeaturesEnabled            bool
	FlakyRetryCount                   int
	TotalFlakyRetryCount              int
}

// CIVisibilitySnapshot returns the process-wide first-use feature snapshot.
func CIVisibilitySnapshot() CIVisibilityConfig {
	value, report := PrepareCIVisibilitySnapshot()
	report()
	return value
}

// PrepareCIVisibilitySnapshot resolves the process-wide feature snapshot and
// returns an idempotent reporter for use after a caller publishes its state.
func PrepareCIVisibilitySnapshot() (CIVisibilityConfig, func()) {
	state := ciVisibilitySnapshotStatePointer.Load()
	if state == nil {
		state = new(ciVisibilitySnapshotState)
		if !ciVisibilitySnapshotStatePointer.CompareAndSwap(nil, state) {
			state = ciVisibilitySnapshotStatePointer.Load()
		}
	}
	state.once.Do(func() {
		state.value, state.events = resolveCIVisibilitySnapshot()
	})
	value := state.value
	report := func() {
		if state.reported.CompareAndSwap(false, true) {
			reportInstrumentationEvents(state.events)
		}
	}
	return value, report
}

// ResetCIVisibilityForTesting clears the first-use feature binding cache.
func ResetCIVisibilityForTesting() {
	ciVisibilitySnapshotStatePointer.Store(new(ciVisibilitySnapshotState))
	bootstrap.ResetTestOptimizationForTesting()
}

func resolveCIVisibilitySnapshot() (CIVisibilityConfig, []ConfigEvent) {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	resolveBoolValue := func(key string, defaultValue bool) bool {
		resolved, local := resolveBoolWithProvider(
			p,
			registeredDefinition(key),
			ciVisibilityFeatureBinding,
			defaultValue,
		)
		events = append(events, local...)
		return resolved.Winner.Value
	}
	resolveIntValue := func(key string, defaultValue int) int {
		resolved, local := resolveCIVisibilityIntWithProvider(
			p,
			registeredDefinition(key),
			ciVisibilityFeatureBinding,
			defaultValue,
		)
		events = append(events, local...)
		return resolved.Winner.Value
	}

	return CIVisibilityConfig{
		GitUploadEnabled:                  resolveBoolValue("DD_CIVISIBILITY_GIT_UPLOAD_ENABLED", true),
		FlakyRetryEnabled:                 resolveBoolValue("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED", true),
		ImpactedTestsEnabled:              resolveBoolValue("DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED", true),
		CodeCoverageReportUploadEnabled:   resolveBoolValue("DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED", true),
		TestManagementEnabled:             resolveBoolValue("DD_TEST_MANAGEMENT_ENABLED", true),
		TestManagementAttemptToFixRetries: resolveIntValue("DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES", -1),
		SubtestFeaturesEnabled:            resolveBoolValue("DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED", true),
		FlakyRetryCount:                   resolveIntValue("DD_CIVISIBILITY_FLAKY_RETRY_COUNT", ciVisibilityDefaultFlakyRetryCount),
		TotalFlakyRetryCount:              resolveIntValue("DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT", ciVisibilityDefaultTotalFlakyRetryCount),
	}, events
}

func resolveCIVisibilityIntWithProvider(
	p *provider.Provider,
	def RawDefinition,
	binding ConsumerBinding,
	defaultValue int,
) (schema.Resolved[int], []ConfigEvent) {
	resolved, events := resolveBoundWithProvider(p, def, binding, defaultValue, strconv.Atoi)
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil && attempt.Origin == telemetry.OriginEnvVar {
			log.Warn(
				"Non-integer value for env var %s, defaulting to %d. Parse failed with error: %v",
				def.Key,
				defaultValue,
				attempt.Err.Error(),
			)
		}
	}
	return resolved, events
}

func resolveCIVisibilityStableBool(
	key string,
	binding ConsumerBinding,
	defaultValue bool,
) (schema.Resolved[bool], []ConfigEvent) {
	resolved, events := resolveBound(
		registeredDefinition(key),
		binding,
		defaultValue,
		strconv.ParseBool,
	)
	return filterEmptyDeclarativeStableAttempts(
		resolved,
		events,
		key,
		defaultValue,
		strconv.ParseBool,
	)
}

// CIVisibilityEnabledMode is the parsed DD_CIVISIBILITY_ENABLED value.
type CIVisibilityEnabledMode uint8

const (
	CIVisibilityEnabledModeDisabled CIVisibilityEnabledMode = iota
	CIVisibilityEnabledModeEnabled
	CIVisibilityEnabledModeParent
)

// ParseCIVisibilityEnabledMode accepts Go boolean values and the parent mode.
func ParseCIVisibilityEnabledMode(raw string) (CIVisibilityEnabledMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "parent" {
		return CIVisibilityEnabledModeParent, nil
	}
	enabled, err := strconv.ParseBool(normalized)
	if err != nil {
		return CIVisibilityEnabledModeDisabled, err
	}
	if enabled {
		return CIVisibilityEnabledModeEnabled, nil
	}
	return CIVisibilityEnabledModeDisabled, nil
}

// PrepareCIVisibilityEnabledMode resolves the environment-only bootstrap gate
// without reporting. The second return value is false for a missing or invalid
// value.
func PrepareCIVisibilityEnabledMode() (CIVisibilityEnabledMode, bool, []ConfigEvent) {
	resolved, events := resolveBound(
		registeredDefinition("DD_CIVISIBILITY_ENABLED"),
		ciVisibilityEnabledBinding,
		CIVisibilityEnabledModeDisabled,
		ParseCIVisibilityEnabledMode,
	)
	return resolved.Winner.Value, !resolved.Winner.DefaultUsed, events
}

// ResolveCIVisibilityEnabledMode samples the environment-only bootstrap gate.
func ResolveCIVisibilityEnabledMode() (CIVisibilityEnabledMode, bool) {
	value, present, events := PrepareCIVisibilityEnabledMode()
	reportInstrumentationEvents(events)
	return value, present
}

// CIVisibilityBootstrapConfig contains values sampled by the one-time CI
// Visibility tracer bootstrap.
type CIVisibilityBootstrapConfig struct {
	DebugEnabled bool
	Service      string
}

// PrepareCIVisibilityBootstrapConfig resolves bootstrap configuration without
// reporting. The caller must publish its initialization state before reporting.
func PrepareCIVisibilityBootstrapConfig() (CIVisibilityBootstrapConfig, []ConfigEvent) {
	debug, debugEvents := resolveCIVisibilityStableBool("DD_TRACE_DEBUG", ciVisibilityBootstrapDebugBinding, false)
	service, serviceEvents := resolveString("DD_SERVICE", ciVisibilityBootstrapServiceBinding)
	return CIVisibilityBootstrapConfig{
		DebugEnabled: debug.Winner.Value,
		Service:      service.Winner.Value,
	}, append(debugEvents, serviceEvents...)
}

// ResolveCIVisibilityBootstrapConfig samples bootstrap configuration.
func ResolveCIVisibilityBootstrapConfig() CIVisibilityBootstrapConfig {
	value, events := PrepareCIVisibilityBootstrapConfig()
	reportInstrumentationEvents(events)
	return value
}

// PrepareCIVisibilityLogsEnabled resolves the first-use logs gate without
// reporting. The caller must publish its cache before reporting the events.
func PrepareCIVisibilityLogsEnabled() (bool, []ConfigEvent) {
	resolved, events := resolveCIVisibilityStableBool("DD_CIVISIBILITY_LOGS_ENABLED", ciVisibilityLogsBinding, false)
	return resolved.Winner.Value, events
}

// CIVisibilityClientConfig contains values sampled for one CI Visibility HTTP
// client constructor.
type CIVisibilityClientConfig struct {
	Environment              string
	Service                  string
	AgentlessEnabled         bool
	APIKey                   string
	AgentlessURL             string
	Site                     string
	CustomTestConfigurations map[string]string
}

// ResolveCIVisibilityClientConfig samples one client constructor.
func ResolveCIVisibilityClientConfig() CIVisibilityClientConfig {
	value, events := PrepareCIVisibilityClientConfig()
	reportInstrumentationEvents(events)
	return value
}

// PrepareCIVisibilityClientConfig samples one client constructor without
// reporting so callers can publish their own initialization state first.
func PrepareCIVisibilityClientConfig() (CIVisibilityClientConfig, []ConfigEvent) {
	value, events := resolveCIVisibilityClientConfig()
	value.CustomTestConfigurations = maps.Clone(value.CustomTestConfigurations)
	return value, events
}

func resolveCIVisibilityClientConfig() (CIVisibilityClientConfig, []ConfigEvent) {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	resolveStringValue := func(key string) string {
		resolved, local := resolveStringWithProvider(
			p,
			registeredDefinition(key),
			ciVisibilityClientBinding,
		)
		events = append(events, local...)
		return resolved.Winner.Value
	}

	environment := resolveStringValue("DD_ENV")
	if environment == "" {
		environment = "none"
	}
	service := resolveStringValue("DD_SERVICE")
	rawTags := resolveStringValue("DD_TAGS")
	custom := make(map[string]string)
	for key, value := range internal.ParseTagString(rawTags) {
		if after, ok := strings.CutPrefix(key, "test.configuration."); ok {
			custom[after] = value
		}
	}
	if len(custom) == 0 {
		custom = nil
	}

	agentless, local := resolveBoolWithProvider(
		p,
		registeredDefinition("DD_CIVISIBILITY_AGENTLESS_ENABLED"),
		ciVisibilityClientBinding,
		false,
	)
	events = append(events, local...)
	value := CIVisibilityClientConfig{
		Environment:              environment,
		Service:                  service,
		AgentlessEnabled:         agentless.Winner.Value,
		CustomTestConfigurations: custom,
	}
	if !value.AgentlessEnabled {
		return value, events
	}

	value.APIKey = resolveStringValue("DD_API_KEY")
	value.AgentlessURL = resolveStringValue("DD_CIVISIBILITY_AGENTLESS_URL")
	if value.AgentlessURL == "" {
		value.Site = resolveStringValue("DD_SITE")
		if value.Site == "" {
			value.Site = "datadoghq.com"
		}
	}
	return value, events
}

// PrepareCIVisibilityTelemetryVersion resolves the version sampled by the
// first CI Visibility telemetry-client initialization without reporting.
func PrepareCIVisibilityTelemetryVersion() (string, []ConfigEvent) {
	resolved, events := resolveString("DD_VERSION", ciVisibilityTelemetryVersionBinding)
	return resolved.Winner.Value, events
}

// CIVisibilityTagConfig contains Datadog configuration overrides sampled when
// CI tags are first constructed.
type CIVisibilityTagConfig struct {
	Service                string
	TestSessionName        string
	TestSessionNamePresent bool
	Overrides              map[string]string
}

var ciVisibilityOverrideKeys = []string{
	"DD_ACTION_EXECUTION_ID",
	"DD_CUSTOM_PARENT_ID",
	"DD_CUSTOM_TRACE_ID",
	"DD_GIT_BRANCH",
	"DD_GIT_COMMIT_AUTHOR_DATE",
	"DD_GIT_COMMIT_AUTHOR_EMAIL",
	"DD_GIT_COMMIT_AUTHOR_NAME",
	"DD_GIT_COMMIT_COMMITTER_DATE",
	"DD_GIT_COMMIT_COMMITTER_EMAIL",
	"DD_GIT_COMMIT_COMMITTER_NAME",
	"DD_GIT_COMMIT_MESSAGE",
	"DD_GIT_COMMIT_SHA",
	"DD_GIT_PULL_REQUEST_BASE_BRANCH",
	"DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA",
	"DD_GIT_REPOSITORY_URL",
	"DD_GIT_TAG",
	"DD_PIPELINE_EXECUTION_ID",
}

// PrepareCIVisibilityTagConfig resolves CI tag overrides without reporting.
// The caller must publish its tag cache before reporting the events.
func PrepareCIVisibilityTagConfig() (CIVisibilityTagConfig, []ConfigEvent) {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	resolve := func(key string) schema.Resolved[string] {
		resolved, local := resolveStringWithProvider(
			p,
			registeredDefinition(key),
			ciVisibilityTagBinding,
		)
		events = append(events, local...)
		return resolved
	}

	service := resolve("DD_SERVICE")
	session := resolve("DD_TEST_SESSION_NAME")
	overrides := make(map[string]string)
	for _, key := range ciVisibilityOverrideKeys {
		resolved := resolve(key)
		if resolved.Winner.Value != "" {
			overrides[key] = resolved.Winner.Value
		}
	}
	return CIVisibilityTagConfig{
		Service:                service.Winner.Value,
		TestSessionName:        session.Winner.Value,
		TestSessionNamePresent: sourcePresent(session.Attempts),
		Overrides:              maps.Clone(overrides),
	}, events
}

// ResolveCIVisibilityTagConfig samples and reports CI tag overrides.
func ResolveCIVisibilityTagConfig() CIVisibilityTagConfig {
	value, events := PrepareCIVisibilityTagConfig()
	reportInstrumentationEvents(events)
	value.Overrides = maps.Clone(value.Overrides)
	return value
}

// CIVisibilityEnvironmentDataFile samples the per-call environmental data file
// path. File contents never enter configuration events.
func CIVisibilityEnvironmentDataFile() string {
	value, events := PrepareCIVisibilityEnvironmentDataFile()
	reportInstrumentationEvents(events)
	return value
}

// PrepareCIVisibilityEnvironmentDataFile resolves the per-call path without
// reporting so cache-owning callers can publish first.
func PrepareCIVisibilityEnvironmentDataFile() (string, []ConfigEvent) {
	resolved, events := resolveString(
		"DD_TEST_OPTIMIZATION_ENV_DATA_FILE",
		ciVisibilityEnvironmentDataBinding,
	)
	return strings.TrimSpace(resolved.Winner.Value), events
}

// ResolveCIVisibilityCoverageReportFlags samples and normalizes coverage report
// flags for one coverage client.
func ResolveCIVisibilityCoverageReportFlags() []string {
	resolved, events := resolveBound(
		registeredDefinition("DD_CODE_COVERAGE_FLAGS"),
		ciVisibilityCoverageFlagsBinding,
		[]string(nil),
		func(raw string) ([]string, error) {
			return ParseCIVisibilityCoverageReportFlags(raw), nil
		},
	)
	reportInstrumentationEvents(events)
	return slices.Clone(resolved.Winner.Value)
}

// ParseCIVisibilityCoverageReportFlags normalizes the comma-separated report
// flag list while preserving order and duplicates.
func ParseCIVisibilityCoverageReportFlags(raw string) []string {
	parts := strings.Split(raw, ",")
	flags := make([]string, 0, len(parts))
	for _, part := range parts {
		if flag := strings.TrimSpace(part); flag != "" {
			flags = append(flags, flag)
		}
	}
	if len(flags) == 0 {
		return nil
	}
	if len(flags) > ciVisibilityMaxCoverageReportFlags {
		log.Warn(
			"civisibility.coverage_report: DD_CODE_COVERAGE_FLAGS contains %d flags, exceeding the maximum of %d; report flags will be omitted",
			len(flags),
			ciVisibilityMaxCoverageReportFlags,
		)
		return nil
	}
	return slices.Clone(flags)
}

// CIVisibilityAutoInstrumentation reports whether an auto-instrumentation
// provider marker is present for this telemetry call.
func CIVisibilityAutoInstrumentation() bool {
	resolved, events := resolveString(
		"DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER",
		ciVisibilityAutoInstrumentationBinding,
	)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value != ""
}

// CIVisibilityParallelEFDEnabled samples the per-test parallel EFD gate.
func CIVisibilityParallelEFDEnabled() bool {
	resolved, events := resolveBool(
		"DD_CIVISIBILITY_INTERNAL_PARALLEL_EARLY_FLAKE_DETECTION_ENABLED",
		ciVisibilityParallelEFDBinding,
		false,
	)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// CIVisibilityTestManagementInstrumentationEnabled samples the constructor
// gate used by Go testing instrumentation.
func CIVisibilityTestManagementInstrumentationEnabled() bool {
	resolved, events := resolveBool(
		"DD_TEST_MANAGEMENT_ENABLED",
		ciVisibilityTestManagementInstrumentationBinding,
		true,
	)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// CIVisibilityTestOptimizationConfig contains Datadog-controlled Bazel mode
// settings sampled when the mode cache is initialized.
type CIVisibilityTestOptimizationConfig struct {
	ManifestFile    string
	PayloadsInFiles bool
	PayloadsRaw     string
	PayloadsPresent bool
}

// PrepareCIVisibilityTestOptimizationConfig resolves Bazel mode settings
// without reporting. The caller must publish its cache before reporting.
func PrepareCIVisibilityTestOptimizationConfig() (CIVisibilityTestOptimizationConfig, []ConfigEvent) {
	snapshot, report := bootstrap.ClaimTestOptimizationTelemetry()
	var events []ConfigEvent
	if report {
		events = append(events,
			ciVisibilityBootstrapEvent(
				"DD_TEST_OPTIMIZATION_MANIFEST_FILE",
				snapshot.ManifestPresent,
				nil,
				snapshot.ManifestFile,
			),
			ciVisibilityBootstrapDefaultEvent("DD_TEST_OPTIMIZATION_MANIFEST_FILE", ""),
			ciVisibilityBootstrapEvent(
				"DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES",
				snapshot.PayloadsPresent,
				snapshot.PayloadsError,
				snapshot.PayloadsInFiles,
			),
			ciVisibilityBootstrapDefaultEvent("DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES", false),
		)
	}
	return CIVisibilityTestOptimizationConfig{
		ManifestFile:    snapshot.ManifestFile,
		PayloadsInFiles: snapshot.PayloadsInFiles,
		PayloadsRaw:     snapshot.PayloadsRaw,
		PayloadsPresent: snapshot.PayloadsPresent,
	}, events
}

func ciVisibilityBootstrapEvent(key string, present bool, err error, value any) ConfigEvent {
	def := registeredDefinition(key)
	return ConfigEvent{
		Kind:          EventConfiguration,
		BindingID:     ciVisibilityTestOptimizationBinding.ID,
		Name:          key,
		Value:         value,
		Present:       present,
		Valid:         present && err == nil,
		Err:           err,
		Origin:        telemetry.OriginEnvVar,
		SourceOrdinal: schema.SourceOrdinalEnvironment,
		Policy:        def.Telemetry,
		Cadence:       ReportOncePerGeneration,
		ReportValue:   present && err == nil,
	}
}

func ciVisibilityBootstrapDefaultEvent(key string, value any) ConfigEvent {
	def := registeredDefinition(key)
	return ConfigEvent{
		Kind:          EventConfiguration,
		BindingID:     ciVisibilityTestOptimizationBinding.ID,
		Name:          key,
		Value:         value,
		Present:       true,
		Valid:         true,
		Origin:        telemetry.OriginDefault,
		SourceOrdinal: schema.SourceOrdinalDefault,
		Policy:        def.Telemetry,
		Cadence:       ReportOncePerGeneration,
		ReportValue:   true,
	}
}

// ReportCIVisibilityConfigEvents reports events after a consumer publishes its
// own cached state.
func ReportCIVisibilityConfigEvents(events []ConfigEvent) {
	reportInstrumentationEvents(events)
}

// CIVisibilityConfigEventsReporter returns nil when the caller does not own
// any events, allowing staged caches to adopt the real reporter later.
func CIVisibilityConfigEventsReporter(events []ConfigEvent) func() {
	if len(events) == 0 {
		return nil
	}
	return func() { ReportCIVisibilityConfigEvents(events) }
}
