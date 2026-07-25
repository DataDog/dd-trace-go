// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/stretchr/testify/require"
)

func TestSystemAgentURLPrecedence(t *testing.T) {
	for name, test := range map[string]struct {
		agentURL string
		host     string
		port     string
		uds      bool
		want     string
	}{
		"http URL":      {agentURL: "http://agent.example:1234", host: "other", port: "5678", want: "http://agent.example:1234"},
		"https URL":     {agentURL: "https://agent.example:1234", host: "other", port: "5678", want: "https://agent.example:1234"},
		"unix URL":      {agentURL: "unix:///path/to/custom.socket", host: "other", port: "5678", want: "unix:///path/to/custom.socket"},
		"host only":     {host: "agent", want: "http://agent:8126"},
		"port only":     {port: "9126", want: "http://localhost:9126"},
		"host and port": {host: "agent", port: "9126", want: "http://agent:9126"},
		"UDS":           {uds: true, want: "unix://UDS_PATH"},
		"nothing":       {want: "http://localhost:8126"},
	} {
		t.Run(name, func(t *testing.T) {
			oldUDSPath := internal.DefaultTraceAgentUDSPath
			if test.uds {
				internal.DefaultTraceAgentUDSPath = t.TempDir()
				test.want = "unix://" + internal.DefaultTraceAgentUDSPath
			} else {
				internal.DefaultTraceAgentUDSPath = filepath.Join(t.TempDir(), "missing.socket")
			}
			t.Cleanup(func() { internal.DefaultTraceAgentUDSPath = oldUDSPath })
			t.Setenv("DD_TRACE_AGENT_URL", test.agentURL)
			t.Setenv("DD_AGENT_HOST", test.host)
			t.Setenv("DD_TRACE_AGENT_PORT", test.port)

			snapshot, _ := ResolveSystemSnapshot()

			require.Equal(t, test.want, snapshot.AgentURL.String())
		})
	}
}

func TestSystemInvalidAgentURLFallsBackAndWarns(t *testing.T) {
	oldUDSPath := internal.DefaultTraceAgentUDSPath
	internal.DefaultTraceAgentUDSPath = filepath.Join(t.TempDir(), "missing.socket")
	t.Cleanup(func() { internal.DefaultTraceAgentUDSPath = oldUDSPath })
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()

	for name, raw := range map[string]string{
		"unsupported": "ftp://agent.example:8126",
		"invalid":     "http://localhost%+o:8126",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", raw)
			t.Setenv("DD_AGENT_HOST", "")
			t.Setenv("DD_TRACE_AGENT_PORT", "")
			logger.Reset()

			snapshot, _ := ResolveSystemSnapshot()

			require.Equal(t, "http://localhost:8126", snapshot.AgentURL.String())
			require.NotEmpty(t, logger.Logs())
		})
	}
}

func TestAgentURLWinnerShortCircuitsHostAndPortReads(t *testing.T) {
	t.Setenv("DD_TRACE_AGENT_URL", "https://agent.example:1234")
	t.Setenv("DD_AGENT_HOST", "ignored-host")
	t.Setenv("DD_TRACE_AGENT_PORT", "not-a-port")

	value, events := resolveAgentURLWithProvider(newEnvironmentProvider())

	require.Equal(t, "https://agent.example:1234", value.String())
	for _, event := range events {
		require.Equal(t, "DD_TRACE_AGENT_URL", event.Name)
	}
}

func TestGitMetadataSnapshotSanitizesBeforeEventsAndPreservesPrecedence(t *testing.T) {
	t.Setenv("DD_TRACE_GIT_METADATA_ENABLED", "true")
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://user:secret@example.com/env.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "")
	t.Setenv("DD_TAGS", "git.repository_url:https://tags.example/repo.git,git.commit.sha:tags-sha,go_path:example/module")

	snapshot, events := ResolveGitMetadataSnapshot()

	require.Equal(t, "https://example.com/env.git", snapshot.Tags[internal.TagRepositoryURL])
	require.Equal(t, "tags-sha", snapshot.Tags[internal.TagCommitSha])
	require.Equal(t, "example/module", snapshot.Tags[internal.TagGoPath])
	require.NotContains(t, fmt.Sprint(events), "secret")
}

func TestGitMetadataSnapshotDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_GIT_METADATA_ENABLED", "false")
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://example.com/repo.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "sha")
	t.Setenv("DD_TAGS", "git.commit.sha:tag-sha")

	snapshot, _ := ResolveGitMetadataSnapshot()

	require.Empty(t, snapshot.Tags)
}

func TestGitMetadataSnapshotCachesUntilExplicitRefresh(t *testing.T) {
	t.Setenv("DD_TRACE_GIT_METADATA_ENABLED", "true")
	t.Setenv("DD_GIT_COMMIT_SHA", "first")
	RefreshGitMetadataForTesting()
	require.Equal(t, "first", GitMetadataSnapshotValue().Tags[internal.TagCommitSha])

	t.Setenv("DD_GIT_COMMIT_SHA", "second")
	require.Equal(t, "first", GitMetadataSnapshotValue().Tags[internal.TagCommitSha])

	RefreshGitMetadataForTesting()
	require.Equal(t, "second", GitMetadataSnapshotValue().Tags[internal.TagCommitSha])
	t.Cleanup(resetGitMetadataCacheForTesting)
}

type reentrantGitMetadataClient struct {
	*telemetrytest.RecordClient
	once sync.Once
}

func (c *reentrantGitMetadataClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.once.Do(func() {
		_ = GitMetadataSnapshotValue()
	})
	c.RecordClient.RegisterAppConfigs(configs...)
}

func prepareGitMetadataReportingTest(t *testing.T, client telemetry.Client) {
	t.Helper()
	bootstrap.ResetForTesting()
	instrumentationReporter.ResetForTesting()
	resetGitMetadataCacheForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(instrumentationReporter.ResetForTesting)
	t.Cleanup(resetGitMetadataCacheForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_TRACE_GIT_METADATA_ENABLED", "true")
	t.Cleanup(telemetry.MockClient(client))
}

func TestGitMetadataFirstUseReportsAfterPublishingCache(t *testing.T) {
	client := &reentrantGitMetadataClient{RecordClient: new(telemetrytest.RecordClient)}
	prepareGitMetadataReportingTest(t, client)
	t.Setenv("DD_GIT_COMMIT_SHA", "reentrant-sha")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = GitMetadataSnapshotValue()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Git metadata reporting deadlocked when the telemetry sink reentered the cache")
	}
	require.NotEmpty(t, client.Configuration)
}

func TestGitMetadataConcurrentFirstUseReportsEnvironmentEventOnce(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	prepareGitMetadataReportingTest(t, client)
	t.Setenv("DD_GIT_COMMIT_SHA", "concurrent-sha")

	var ready sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		ready.Add(1)
		go func() {
			defer ready.Done()
			<-start
			_ = GitMetadataSnapshotValue()
		}()
	}
	close(start)
	ready.Wait()

	var matches int
	for _, configuration := range client.Configuration {
		if configuration.Name == "DD_GIT_COMMIT_SHA" &&
			configuration.Origin == telemetry.OriginEnvVar &&
			configuration.Value == "concurrent-sha" {
			matches++
		}
	}
	require.Equal(t, 1, matches)
}

func TestInstallInfoSnapshotResamplesAtEachUseBoundary(t *testing.T) {
	t.Setenv("DD_INSTRUMENTATION_INSTALL_ID", "first")
	require.Equal(t, "first", ResolveInstallInfoSnapshot().ID)

	t.Setenv("DD_INSTRUMENTATION_INSTALL_ID", "second")
	require.Equal(t, "second", ResolveInstallInfoSnapshot().ID)
}

func TestTelemetrySnapshotDefaultsAndSecretsStayOutOfEvents(t *testing.T) {
	t.Setenv("DD_API_KEY", "secret-api-key")
	t.Setenv("DD_SITE", "datadoghq.eu")
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "")
	t.Setenv("DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", "")

	snapshot, events := ResolveTelemetrySnapshot()

	require.Equal(t, "secret-api-key", snapshot.APIKey)
	require.Equal(t, "datadoghq.eu", snapshot.Site)
	require.Equal(t, 60.0, snapshot.HeartbeatIntervalSeconds)
	require.Equal(t, 24*time.Hour.Seconds(), snapshot.ExtendedHeartbeatIntervalSeconds)
	require.True(t, snapshot.DependencyCollectionEnabled)
	require.True(t, snapshot.MetricsEnabled)
	require.True(t, snapshot.LogsEnabled)
	require.NotContains(t, fmt.Sprint(events), "secret-api-key")
}

func TestTelemetrySnapshotParsesOverrides(t *testing.T) {
	t.Setenv("DD_TELEMETRY_DEBUG", "true")
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "false")
	t.Setenv("DD_TELEMETRY_METRICS_ENABLED", "false")
	t.Setenv("DD_TELEMETRY_LOG_COLLECTION_ENABLED", "false")
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "120")
	t.Setenv("DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", "90")

	snapshot, _ := ResolveTelemetrySnapshot()

	require.True(t, snapshot.Debug)
	require.False(t, snapshot.DependencyCollectionEnabled)
	require.False(t, snapshot.MetricsEnabled)
	require.False(t, snapshot.LogsEnabled)
	require.Equal(t, 120.0, snapshot.HeartbeatIntervalSeconds)
	require.Equal(t, 90.0, snapshot.ExtendedHeartbeatIntervalSeconds)
}

func TestTelemetrySnapshotInvalidValuesUseLegacyFallbacksAndWarnings(t *testing.T) {
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "not-a-number")
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "not-a-bool")

	snapshot, _ := ResolveTelemetrySnapshot()

	require.Equal(t, 60.0, snapshot.HeartbeatIntervalSeconds)
	require.True(t, snapshot.DependencyCollectionEnabled)
	logs := strings.Join(logger.Logs(), "\n")
	require.Contains(t, logs, "Non-float value for env var DD_TELEMETRY_HEARTBEAT_INTERVAL")
	require.Contains(t, logs, "Non-boolean value for env var DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED")
}

func TestTelemetrySnapshotInvalidIntervalsUseClientDefaultsInWarnings(t *testing.T) {
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	t.Setenv("DD_TELEMETRY_HEARTBEAT_INTERVAL", "not-a-number")
	t.Setenv("DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL", "also-not-a-number")

	snapshot, _ := resolveTelemetrySnapshot(20*time.Second, 2*time.Hour)

	require.Equal(t, 20.0, snapshot.HeartbeatIntervalSeconds)
	require.Equal(t, 2*time.Hour.Seconds(), snapshot.ExtendedHeartbeatIntervalSeconds)
	logs := strings.Join(logger.Logs(), "\n")
	require.Contains(t, logs, "DD_TELEMETRY_HEARTBEAT_INTERVAL, defaulting to 20.000000")
	require.Contains(t, logs, "DD_TELEMETRY_EXTENDED_HEARTBEAT_INTERVAL, defaulting to 7200.000000")
}

func TestConfigureTelemetryClientSkipsInactiveEnvironmentReads(t *testing.T) {
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	bootstrap.ResetForTesting()
	instrumentationReporter.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(instrumentationReporter.ResetForTesting)
	client := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(client))
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	const secret = "inactive-api-key-sentinel"
	t.Setenv("DD_SITE", "inactive.example")
	t.Setenv("DD_API_KEY", secret)
	t.Setenv("DD_TELEMETRY_DEBUG", "not-a-bool")
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "not-a-bool")
	t.Setenv("DD_TELEMETRY_METRICS_ENABLED", "not-a-bool")
	t.Setenv("DD_TELEMETRY_LOG_COLLECTION_ENABLED", "not-a-bool")
	clientConfig := telemetry.ClientConfig{
		AgentlessURL: "https://programmatic.example/telemetry",
		APIKey:       "programmatic-api-key",
		Debug:        true,
		DependencyLoader: func() (*debug.BuildInfo, bool) {
			return nil, false
		},
		MetricsEnabled: true,
		LogsEnabled:    true,
	}

	ConfigureTelemetryClient(&clientConfig)

	require.NotEmpty(t, client.Configuration)
	for _, inactive := range []string{
		"DD_SITE",
		"DD_API_KEY",
		"DD_TELEMETRY_DEBUG",
		"DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED",
		"DD_TELEMETRY_METRICS_ENABLED",
		"DD_TELEMETRY_LOG_COLLECTION_ENABLED",
	} {
		for _, configuration := range client.Configuration {
			require.NotEqual(t, inactive, configuration.Name)
		}
	}
	require.NotContains(t, fmt.Sprintf("%#v", client.Configuration), secret)
	require.Empty(t, logger.Logs())
}

func TestRemoteConfigSnapshotConstructorValues(t *testing.T) {
	t.Setenv("DD_ENV", "prod")
	t.Setenv("DD_RC_TUF_ROOT", filepath.Join(t.TempDir(), "root.json"))
	t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", "0")

	snapshot := ResolveRemoteConfigSnapshot()

	require.Equal(t, "prod", snapshot.Env)
	require.NotEmpty(t, snapshot.TUFRoot)
	require.Equal(t, time.Nanosecond, snapshot.PollInterval)
}

func TestRemoteConfigEnabledResamplesAtStart(t *testing.T) {
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	require.False(t, RemoteConfigEnabled())
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "true")
	require.True(t, RemoteConfigEnabled())
}

func TestRemoteConfigInvalidAndNegativeIntervalsUseFiveSeconds(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid":  "not-a-number",
		"negative": "-1",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_REMOTE_CONFIG_POLL_INTERVAL_SECONDS", raw)
			require.Equal(t, 5*time.Second, ResolveRemoteConfigSnapshot().PollInterval)
		})
	}
}

func TestHostnameConfigResamplesForProviderRefresh(t *testing.T) {
	t.Setenv("DD_HOSTNAME", "first.example")
	require.Equal(t, "first.example", HostnameConfig())
	t.Setenv("DD_HOSTNAME", "second.example")
	require.Equal(t, "second.example", HostnameConfig())
}

func TestProcessTagsEnabledResamplesOnReload(t *testing.T) {
	t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
	require.False(t, ProcessTagsEnabled())
	t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "true")
	require.True(t, ProcessTagsEnabled())
}

func TestProcessTagsReloadUsesFreshConfig(t *testing.T) {
	t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
	processtags.Reload()
	require.Nil(t, processtags.GlobalTags())

	t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "true")
	processtags.Reload()
	require.NotNil(t, processtags.GlobalTags())
}

func TestAppEndpointsMessageLimitPreservesDefaultAndParsing(t *testing.T) {
	require.Equal(t, 300, appEndpointsMessageLimit())

	t.Setenv("DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT", "17")
	require.Equal(t, 17, appEndpointsMessageLimit())

	t.Setenv("DD_API_SECURITY_ENDPOINT_COLLECTION_MESSAGE_LIMIT", "invalid")
	require.Equal(t, 300, appEndpointsMessageLimit())
}

func TestLoggingRateExplicitEmptyIsIgnoredWithoutWarning(t *testing.T) {
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	t.Setenv("DD_LOGGING_RATE", "")

	applyLoggingRate()

	require.Empty(t, logger.Logs())
}

func TestSystemDefinitionsUseEnvironmentOnlyBindings(t *testing.T) {
	_, bindings := RegisteredDefinitions()
	byID := make(map[string]ConsumerBinding, len(bindings))
	for _, binding := range bindings {
		byID[binding.ID] = binding
	}

	for _, binding := range []ConsumerBinding{
		systemAgentURLBinding,
		systemExternalEnvironmentBinding,
		systemGitMetadataBinding,
		systemHostnameBinding,
		systemInstallInfoBinding,
		systemLoggingRateBinding,
		systemProcessTagsBinding,
		systemRemoteConfigDefaultsBinding,
		systemRemoteConfigEnabledBinding,
		systemTelemetryBinding,
		systemAppEndpointsBinding,
	} {
		require.Equal(t, binding, byID[binding.ID], binding.ID)
	}
}
