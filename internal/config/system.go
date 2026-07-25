// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"maps"
	"net/url"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/hostname"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/urlsanitizer"
)

var (
	systemAgentURLBinding = ConsumerBinding{
		ID: "system.agent-url", Consumer: "shared agent clients",
		Keys:     []string{"DD_TRACE_AGENT_URL", "DD_AGENT_HOST", "DD_TRACE_AGENT_PORT"},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	}
	systemExternalEnvironmentBinding = ConsumerBinding{
		ID: "system.external-environment", Consumer: "tracer transport headers",
		Keys: []string{"DD_EXTERNAL_ENV"}, Sampling: SampleTracerConstruction, EnvironmentOnly: true,
	}
	systemGitMetadataBinding = ConsumerBinding{
		ID: "system.git-metadata", Consumer: "shared git metadata",
		Keys: []string{
			"DD_TRACE_GIT_METADATA_ENABLED",
			"DD_GIT_REPOSITORY_URL",
			"DD_GIT_COMMIT_SHA",
			"DD_TAGS",
		},
		Sampling: SampleFirstUse, EnvironmentOnly: true,
	}
	systemHostnameBinding = ConsumerBinding{
		ID: "system.hostname", Consumer: "hostname provider",
		Keys: []string{"DD_HOSTNAME"}, Sampling: SamplePerCall, EnvironmentOnly: true,
	}
	systemInstallInfoBinding = ConsumerBinding{
		ID: "system.install-info", Consumer: "telemetry install signature",
		Keys: []string{
			"DD_INSTRUMENTATION_INSTALL_ID",
			"DD_INSTRUMENTATION_INSTALL_TYPE",
			"DD_INSTRUMENTATION_INSTALL_TIME",
		},
		Sampling: SamplePerCall, EnvironmentOnly: true,
	}
	systemLoggingRateBinding = ConsumerBinding{
		ID: "system.logging-rate", Consumer: "internal/log package initialization",
		Keys: []string{"DD_LOGGING_RATE"}, Sampling: SamplePackageInit, EnvironmentOnly: true,
	}
	systemProcessTagsBinding = ConsumerBinding{
		ID: "system.process-tags", Consumer: "internal/processtags reload",
		Keys:     []string{"DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED"},
		Sampling: SamplePerCall, EnvironmentOnly: true,
	}
	systemRemoteConfigDefaultsBinding = ConsumerBinding{
		ID: "system.remote-config-defaults", Consumer: "remoteconfig.DefaultClientConfig",
		Keys:     []string{"DD_ENV", "DD_RC_TUF_ROOT", "DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS"},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	}
	systemRemoteConfigEnabledBinding = ConsumerBinding{
		ID: "system.remote-config-enabled", Consumer: "remoteconfig.Start",
		Keys:     []string{"DD_REMOTE_CONFIGURATION_ENABLED"},
		Sampling: SampleProductStart, EnvironmentOnly: true,
	}
	systemTelemetryBinding = ConsumerBinding{
		ID: "system.telemetry-client", Consumer: "telemetry.NewClient",
		Keys: []string{
			"DD_API_KEY",
			"DD_SITE",
			"DD_TELEMETRY_DEBUG",
			"DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED",
			"DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL",
			"DD_TELEMETRY_HEARTBEAT_INTERVAL",
			"DD_TELEMETRY_LOG_COLLECTION_ENABLED",
			"DD_TELEMETRY_METRICS_ENABLED",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	}
	systemAppEndpointsBinding = ConsumerBinding{
		ID: "system.app-endpoints", Consumer: "telemetry app endpoint buffer",
		Keys:     []string{"DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT"},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	}

	gitMetadataCache struct {
		sync.Mutex
		initialized bool
		value       GitMetadataSnapshot
	}
)

func init() {
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_EXTERNAL_ENV", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_GIT_COMMIT_SHA", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_GIT_REPOSITORY_URL", Sources: SourceEnvironment, Telemetry: TelemetrySanitizeURL})
	registerRaw(RawDefinition{Key: "DD_HOSTNAME", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_INSTRUMENTATION_INSTALL_ID", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_INSTRUMENTATION_INSTALL_TIME", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_INSTRUMENTATION_INSTALL_TYPE", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_LOGGING_RATE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_RC_TUF_ROOT", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_REMOTE_CONFIGURATION_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TELEMETRY_DEBUG", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TELEMETRY_HEARTBEAT_INTERVAL", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TELEMETRY_LOG_COLLECTION_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TELEMETRY_METRICS_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_GIT_METADATA_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})

	registerBinding(ConsumerBinding{ID: "system.agent-url", Consumer: "shared agent clients", Keys: []string{"DD_TRACE_AGENT_URL", "DD_AGENT_HOST", "DD_TRACE_AGENT_PORT"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.external-environment", Consumer: "tracer transport headers", Keys: []string{"DD_EXTERNAL_ENV"}, Sampling: SampleTracerConstruction, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.git-metadata", Consumer: "shared git metadata", Keys: []string{"DD_TRACE_GIT_METADATA_ENABLED", "DD_GIT_REPOSITORY_URL", "DD_GIT_COMMIT_SHA", "DD_TAGS"}, Sampling: SampleFirstUse, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.hostname", Consumer: "hostname provider", Keys: []string{"DD_HOSTNAME"}, Sampling: SamplePerCall, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.install-info", Consumer: "telemetry install signature", Keys: []string{"DD_INSTRUMENTATION_INSTALL_ID", "DD_INSTRUMENTATION_INSTALL_TYPE", "DD_INSTRUMENTATION_INSTALL_TIME"}, Sampling: SamplePerCall, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.logging-rate", Consumer: "internal/log package initialization", Keys: []string{"DD_LOGGING_RATE"}, Sampling: SamplePackageInit, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.process-tags", Consumer: "internal/processtags reload", Keys: []string{"DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED"}, Sampling: SamplePerCall, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.remote-config-defaults", Consumer: "remoteconfig.DefaultClientConfig", Keys: []string{"DD_ENV", "DD_RC_TUF_ROOT", "DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.remote-config-enabled", Consumer: "remoteconfig.Start", Keys: []string{"DD_REMOTE_CONFIGURATION_ENABLED"}, Sampling: SampleProductStart, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.telemetry-client", Consumer: "telemetry.NewClient", Keys: []string{"DD_API_KEY", "DD_SITE", "DD_TELEMETRY_DEBUG", "DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", "DD_TELEMETRY_HEARTBEAT_INTERVAL", "DD_TELEMETRY_LOG_COLLECTION_ENABLED", "DD_TELEMETRY_METRICS_ENABLED"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "system.app-endpoints", Consumer: "telemetry app endpoint buffer", Keys: []string{"DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT"}, Sampling: SamplePackageInit, EnvironmentOnly: true})

	initializeSystemPackageSettings()
}

func initializeSystemPackageSettings() {
	applyLoggingRate()
	processtags.SetEnabledProvider(ProcessTagsEnabled)
	processtags.ReloadWithEnabled(ProcessTagsEnabled())
	hostname.SetConfigProvider(HostnameConfig)
	telemetry.SetAppEndpointsMessageLimit(appEndpointsMessageLimit())
}

// SystemSnapshot contains shared process settings that are sampled by a
// constructor.
type SystemSnapshot struct {
	AgentURL            *url.URL
	ExternalEnvironment string
}

// ResolveSystemSnapshot samples the shared constructor settings without
// reporting them. Callers that need only one field should use the targeted
// accessors below.
func ResolveSystemSnapshot() (SystemSnapshot, []ConfigEvent) {
	p := newEnvironmentProvider()
	agentURL, agentEvents := resolveAgentURLWithProvider(p)
	external, externalEvents := resolveStringWithProvider(
		p,
		registeredDefinition("DD_EXTERNAL_ENV"),
		systemExternalEnvironmentBinding,
	)
	return SystemSnapshot{
		AgentURL:            agentURL,
		ExternalEnvironment: external.Winner.Value,
	}, append(agentEvents, externalEvents...)
}

func resolveAgentURLWithProvider(p *provider.Provider) (*url.URL, []ConfigEvent) {
	var events []ConfigEvent
	resolve := func(key string) string {
		resolved, local := resolveStringWithProvider(
			p,
			registeredDefinition(key),
			systemAgentURLBinding,
		)
		events = append(events, local...)
		return resolved.Winner.Value
	}
	rawURL := resolve("DD_TRACE_AGENT_URL")
	if rawURL != "" {
		if value, valid := parseAgentURL(rawURL); valid {
			return value, events
		}
	}
	return resolveAgentURL("", resolve("DD_AGENT_HOST"), resolve("DD_TRACE_AGENT_PORT")), events
}

// AgentURL samples the environment-only shared agent address.
func AgentURL() *url.URL {
	value, events := resolveAgentURLWithProvider(newEnvironmentProvider())
	reportInstrumentationEvents(events)
	return value
}

// TransportExternalEnvironment samples the external-environment transport header.
func TransportExternalEnvironment() string {
	resolved, events := resolveString("DD_EXTERNAL_ENV", systemExternalEnvironmentBinding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// GitMetadataSnapshot is an immutable view of process git tags.
type GitMetadataSnapshot struct {
	Tags map[string]string
}

// ResolveGitMetadataSnapshot performs a fresh environment and build-info
// sample. Repository credentials are removed before the value leaves the
// provider boundary.
func ResolveGitMetadataSnapshot() (GitMetadataSnapshot, []ConfigEvent) {
	p := newEnvironmentProvider()
	enabled, events := resolveBoolWithProvider(
		p,
		registeredDefinition("DD_TRACE_GIT_METADATA_ENABLED"),
		systemGitMetadataBinding,
		true,
	)
	if !enabled.Winner.Value {
		return GitMetadataSnapshot{Tags: map[string]string{}}, events
	}

	repository, local := resolveBoundWithProvider(
		p,
		registeredDefinition("DD_GIT_REPOSITORY_URL"),
		systemGitMetadataBinding,
		"",
		func(raw string) (string, error) {
			return urlsanitizer.SanitizeURL(raw), nil
		},
	)
	events = append(events, local...)
	commit, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_GIT_COMMIT_SHA"),
		systemGitMetadataBinding,
	)
	events = append(events, local...)
	rawTags, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_TAGS"),
		systemGitMetadataBinding,
	)
	events = append(events, local...)

	tags := make(map[string]string)
	updateGitTag(tags, internal.TagRepositoryURL, repository.Winner.Value)
	updateGitTag(tags, internal.TagCommitSha, commit.Winner.Value)
	ddTags := internal.ParseTagString(rawTags.Winner.Value)
	updateGitTag(tags, internal.TagRepositoryURL, urlsanitizer.SanitizeURL(ddTags[internal.TagRepositoryURL]))
	updateGitTag(tags, internal.TagCommitSha, ddTags[internal.TagCommitSha])
	updateGitTag(tags, internal.TagGoPath, ddTags[internal.TagGoPath])
	for key, value := range binaryGitMetadata(debug.ReadBuildInfo) {
		updateGitTag(tags, key, value)
	}
	return GitMetadataSnapshot{Tags: tags}, events
}

func updateGitTag(tags map[string]string, key, value string) {
	if _, exists := tags[key]; !exists && value != "" {
		tags[key] = value
	}
}

func binaryGitMetadata(readBuildInfo func() (*debug.BuildInfo, bool)) map[string]string {
	info, ok := readBuildInfo()
	if !ok {
		log.Debug("ReadBuildInfo failed, skip source code metadata extracting")
		return map[string]string{}
	}
	var vcs, commit string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs":
			vcs = setting.Value
		case "vcs.revision":
			commit = setting.Value
		}
	}
	if vcs != "git" {
		log.Debug("Unknown VCS: '%s', skip source code metadata extracting", vcs)
		return map[string]string{}
	}
	return map[string]string{
		internal.TagCommitSha: commit,
		internal.TagGoPath:    info.Path,
	}
}

// GitMetadataSnapshotValue returns the first-use process snapshot.
func GitMetadataSnapshotValue() GitMetadataSnapshot {
	gitMetadataCache.Lock()
	if !gitMetadataCache.initialized {
		value, events := ResolveGitMetadataSnapshot()
		gitMetadataCache.value = GitMetadataSnapshot{Tags: maps.Clone(value.Tags)}
		gitMetadataCache.initialized = true
		result := GitMetadataSnapshot{Tags: maps.Clone(gitMetadataCache.value.Tags)}
		gitMetadataCache.Unlock()
		reportInstrumentationEvents(events)
		return result
	}
	result := GitMetadataSnapshot{Tags: maps.Clone(gitMetadataCache.value.Tags)}
	gitMetadataCache.Unlock()
	return result
}

// RefreshGitMetadataForTesting replaces the cached git metadata with a fresh
// sample. It is not safe to race with production readers.
func RefreshGitMetadataForTesting() {
	value, events := ResolveGitMetadataSnapshot()
	gitMetadataCache.Lock()
	gitMetadataCache.value = GitMetadataSnapshot{Tags: maps.Clone(value.Tags)}
	gitMetadataCache.initialized = true
	gitMetadataCache.Unlock()
	reportInstrumentationEvents(events)
}

func resetGitMetadataCacheForTesting() {
	gitMetadataCache.Lock()
	gitMetadataCache.value = GitMetadataSnapshot{}
	gitMetadataCache.initialized = false
	gitMetadataCache.Unlock()
}

// InstallInfoSnapshot is one telemetry install-signature sample.
type InstallInfoSnapshot struct {
	ID   string
	Type string
	Time string
}

// ResolveInstallInfoSnapshot samples install metadata at its telemetry use
// boundary.
func ResolveInstallInfoSnapshot() InstallInfoSnapshot {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	resolve := func(key string) string {
		value, local := resolveStringWithProvider(
			p,
			registeredDefinition(key),
			systemInstallInfoBinding,
		)
		events = append(events, local...)
		return value.Winner.Value
	}
	snapshot := InstallInfoSnapshot{
		ID:   resolve("DD_INSTRUMENTATION_INSTALL_ID"),
		Type: resolve("DD_INSTRUMENTATION_INSTALL_TYPE"),
		Time: resolve("DD_INSTRUMENTATION_INSTALL_TIME"),
	}
	reportInstrumentationEvents(events)
	return snapshot
}

// TelemetrySnapshot is the environment view applied to one telemetry client.
type TelemetrySnapshot struct {
	APIKey                           string
	Site                             string
	Debug                            bool
	DependencyCollectionEnabled      bool
	MetricsEnabled                   bool
	LogsEnabled                      bool
	HeartbeatIntervalSeconds         float64
	HeartbeatIntervalSet             bool
	ExtendedHeartbeatIntervalSeconds float64
	ExtendedHeartbeatIntervalSet     bool
}

// ResolveTelemetrySnapshot samples telemetry client defaults without reporting.
func ResolveTelemetrySnapshot() (TelemetrySnapshot, []ConfigEvent) {
	return resolveTelemetrySnapshotWithRequest(telemetrySnapshotRequest{
		apiKey:                      true,
		site:                        true,
		debug:                       true,
		dependencyCollectionEnabled: true,
		metricsEnabled:              true,
		logsEnabled:                 true,
		heartbeatDefault:            time.Minute,
		extendedHeartbeatDefault:    24 * time.Hour,
	})
}

func resolveTelemetrySnapshot(
	heartbeatDefault time.Duration,
	extendedHeartbeatDefault time.Duration,
) (TelemetrySnapshot, []ConfigEvent) {
	return resolveTelemetrySnapshotWithRequest(telemetrySnapshotRequest{
		apiKey:                      true,
		site:                        true,
		debug:                       true,
		dependencyCollectionEnabled: true,
		metricsEnabled:              true,
		logsEnabled:                 true,
		heartbeatDefault:            heartbeatDefault,
		extendedHeartbeatDefault:    extendedHeartbeatDefault,
	})
}

type telemetrySnapshotRequest struct {
	apiKey                      bool
	site                        bool
	debug                       bool
	dependencyCollectionEnabled bool
	metricsEnabled              bool
	logsEnabled                 bool
	heartbeatDefault            time.Duration
	extendedHeartbeatDefault    time.Duration
}

func resolveTelemetrySnapshotForClient(clientConfig telemetry.ClientConfig) (TelemetrySnapshot, []ConfigEvent) {
	heartbeatDefault := clientConfig.HeartbeatInterval
	if heartbeatDefault == 0 {
		heartbeatDefault = time.Minute
	}
	extendedHeartbeatDefault := clientConfig.ExtendedHeartbeatInterval
	if extendedHeartbeatDefault == 0 {
		extendedHeartbeatDefault = 24 * time.Hour
	}
	return resolveTelemetrySnapshotWithRequest(telemetrySnapshotRequest{
		apiKey:                      clientConfig.APIKey == "",
		site:                        clientConfig.AgentlessURL == "",
		debug:                       !clientConfig.Debug,
		dependencyCollectionEnabled: clientConfig.DependencyLoader == nil,
		metricsEnabled:              !clientConfig.MetricsEnabled,
		logsEnabled:                 !clientConfig.LogsEnabled,
		heartbeatDefault:            heartbeatDefault,
		extendedHeartbeatDefault:    extendedHeartbeatDefault,
	})
}

func resolveTelemetrySnapshotWithRequest(request telemetrySnapshotRequest) (TelemetrySnapshot, []ConfigEvent) {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	resolveStringField := func(key string) string {
		value, local := resolveStringWithProvider(
			p,
			registeredDefinition(key),
			systemTelemetryBinding,
		)
		events = append(events, local...)
		return value.Winner.Value
	}
	resolveBoolField := func(key string, defaultValue bool) bool {
		value, local := resolveBoolWithProvider(
			p,
			registeredDefinition(key),
			systemTelemetryBinding,
			defaultValue,
		)
		events = append(events, local...)
		return value.Winner.Value
	}
	resolveFloatField := func(key string, defaultValue float64) schema.Resolved[float64] {
		value, local := resolveFloatWithProvider(
			p,
			registeredDefinition(key),
			systemTelemetryBinding,
			defaultValue,
		)
		events = append(events, local...)
		return value
	}

	var snapshot TelemetrySnapshot
	if request.site {
		snapshot.Site = resolveStringField("DD_SITE")
		if snapshot.Site == "" {
			snapshot.Site = "datadoghq.com"
		}
	}
	heartbeat := resolveFloatField("DD_TELEMETRY_HEARTBEAT_INTERVAL", request.heartbeatDefault.Seconds())
	snapshot.HeartbeatIntervalSeconds = heartbeat.Winner.Value
	snapshot.HeartbeatIntervalSet = sourceValid(heartbeat.Attempts)
	extendedHeartbeat := resolveFloatField("DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", request.extendedHeartbeatDefault.Seconds())
	snapshot.ExtendedHeartbeatIntervalSeconds = extendedHeartbeat.Winner.Value
	snapshot.ExtendedHeartbeatIntervalSet = sourceValid(extendedHeartbeat.Attempts)
	if request.apiKey {
		snapshot.APIKey = resolveStringField("DD_API_KEY")
	}
	if request.debug {
		snapshot.Debug = resolveBoolField("DD_TELEMETRY_DEBUG", false)
	}
	if request.dependencyCollectionEnabled {
		snapshot.DependencyCollectionEnabled = resolveBoolField("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", true)
	}
	if request.metricsEnabled {
		snapshot.MetricsEnabled = resolveBoolField("DD_TELEMETRY_METRICS_ENABLED", true)
	}
	if request.logsEnabled {
		snapshot.LogsEnabled = resolveBoolField("DD_TELEMETRY_LOG_COLLECTION_ENABLED", true)
	}
	return snapshot, events
}

func resolveFloatWithProvider(
	p *provider.Provider,
	def RawDefinition,
	binding ConsumerBinding,
	defaultValue float64,
) (schema.Resolved[float64], []ConfigEvent) {
	resolved, events := resolveBoundWithProvider(p, def, binding, defaultValue, func(raw string) (float64, error) {
		return strconv.ParseFloat(raw, 64)
	})
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil && attempt.Origin == telemetry.OriginEnvVar {
			log.Warn(
				"Non-float value for env var %s, defaulting to %f. Parse failed with error: %v",
				def.Key,
				defaultValue,
				attempt.Err.Error(),
			)
		}
	}
	return resolved, events
}

// ConfigureTelemetryClient applies one environment snapshot and installs the
// install-info provider used at writer and app-started boundaries.
func ConfigureTelemetryClient(clientConfig *telemetry.ClientConfig) {
	snapshot, events := resolveTelemetrySnapshotForClient(*clientConfig)
	telemetry.SetEnvironmentConfig(clientConfig, telemetry.EnvironmentConfig{
		APIKey:                           snapshot.APIKey,
		Site:                             snapshot.Site,
		Debug:                            snapshot.Debug,
		DependencyCollectionEnabled:      snapshot.DependencyCollectionEnabled,
		MetricsEnabled:                   snapshot.MetricsEnabled,
		LogsEnabled:                      snapshot.LogsEnabled,
		HeartbeatIntervalSeconds:         snapshot.HeartbeatIntervalSeconds,
		HeartbeatIntervalSet:             snapshot.HeartbeatIntervalSet,
		ExtendedHeartbeatIntervalSeconds: snapshot.ExtendedHeartbeatIntervalSeconds,
		ExtendedHeartbeatIntervalSet:     snapshot.ExtendedHeartbeatIntervalSet,
	})
	telemetry.SetInstallInfoProvider(clientConfig, func() telemetry.InstallInfo {
		info := ResolveInstallInfoSnapshot()
		return telemetry.InstallInfo{ID: info.ID, Type: info.Type, Time: info.Time}
	})
	reportInstrumentationEvents(events)
}

// RemoteConfigSnapshot is sampled by each remote-config client constructor.
type RemoteConfigSnapshot struct {
	Env          string
	TUFRoot      string
	PollInterval time.Duration
}

// ResolveRemoteConfigSnapshot samples constructor-scoped remote-config values.
func ResolveRemoteConfigSnapshot() RemoteConfigSnapshot {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	envValue, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_ENV"),
		systemRemoteConfigDefaultsBinding,
	)
	events = append(events, local...)
	tufRoot, local := resolveStringWithProvider(
		p,
		registeredDefinition("DD_RC_TUF_ROOT"),
		systemRemoteConfigDefaultsBinding,
	)
	events = append(events, local...)
	interval, local := resolveFloatWithProvider(
		p,
		registeredDefinition("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS"),
		systemRemoteConfigDefaultsBinding,
		5,
	)
	events = append(events, local...)

	pollInterval := interval.Winner.Value
	if pollInterval < 0 {
		log.Debug(
			"Remote config: cannot use a negative poll interval: DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS = %f. Defaulting to 5s.",
			pollInterval,
		)
		pollInterval = 5
	} else if pollInterval == 0 {
		log.Debug("Remote config: poll interval set to 0. Polling will be continuous.")
		reportInstrumentationEvents(events)
		return RemoteConfigSnapshot{
			Env: envValue.Winner.Value, TUFRoot: tufRoot.Winner.Value, PollInterval: time.Nanosecond,
		}
	}
	reportInstrumentationEvents(events)
	return RemoteConfigSnapshot{
		Env:          envValue.Winner.Value,
		TUFRoot:      tufRoot.Winner.Value,
		PollInterval: time.Duration(pollInterval * float64(time.Second)),
	}
}

// RemoteConfigEnabled samples the start-scoped remote-config gate.
func RemoteConfigEnabled() bool {
	resolved, events := resolveBool(
		"DD_REMOTE_CONFIGURATION_ENABLED",
		systemRemoteConfigEnabledBinding,
		true,
	)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// HostnameConfig samples the configured hostname when the hostname provider
// refreshes.
func HostnameConfig() string {
	resolved, events := resolveString("DD_HOSTNAME", systemHostnameBinding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

// ProcessTagsEnabled samples the process-tag gate for one reload.
func ProcessTagsEnabled() bool {
	resolved, events := resolveBool(
		"DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED",
		systemProcessTagsBinding,
		true,
	)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

func loggingRate() (string, bool) {
	resolved, events := resolveString("DD_LOGGING_RATE", systemLoggingRateBinding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value, sourcePresent(resolved.Attempts)
}

func applyLoggingRate() {
	rate, _ := loggingRate()
	if rate != "" {
		log.SetLoggingRate(rate)
	}
}

func appEndpointsMessageLimit() int {
	def := registeredDefinition("DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT")
	resolved, events := resolveBound(
		def,
		systemAppEndpointsBinding,
		300,
		func(raw string) (int, error) {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return 0, err
			}
			return value, nil
		},
	)
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil && attempt.Origin == telemetry.OriginEnvVar {
			log.Warn(
				"Invalid value for DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT (expected an integer): %s",
				attempt.Err.Error(),
			)
		}
	}
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}
