// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"maps"
	"net/url"
	"runtime"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	defaultProfilerUploadTimeout       = 10 * time.Second
	defaultProfilerExecutionTraceLimit = 5 * 1024 * 1024
)

var (
	profilerEnablementBinding = ConsumerBinding{
		ID: "profiler.enablement", Consumer: "profiler.Start",
		Keys: []string{"DD_PROFILING_ENABLED"}, Sampling: SampleProductStart,
	}
	profilerStartBinding = ConsumerBinding{
		ID: "profiler.start", Consumer: "profiler.Start",
		Keys: []string{
			"DD_API_KEY",
			"DD_ENV",
			"DD_PROFILING_AGENTLESS",
			"DD_PROFILING_DEBUG_COMPRESSION_SETTINGS",
			"DD_PROFILING_DELTA",
			"DD_PROFILING_ENDPOINT_COUNT_ENABLED",
			"DD_PROFILING_EXECUTION_TRACE_ENABLED",
			"DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES",
			"DD_PROFILING_EXECUTION_TRACE_PERIOD",
			"DD_PROFILING_FLUSH_ON_EXIT",
			"DD_PROFILING_OUTPUT_DIR",
			"DD_PROFILING_UPLOAD_TIMEOUT",
			"DD_PROFILING_URL",
			"DD_SERVICE",
			"DD_SITE",
			"DD_TAGS",
			"DD_TRACE_STARTUP_LOGS",
			"DD_VERSION",
		},
		Sampling: SampleProductStart, EnvironmentOnly: true,
	}
)

func init() {
	registerRaw(RawDefinition{Key: "DD_PROFILING_AGENTLESS", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_DELTA", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_ENDPOINT_COUNT_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_EXECUTION_TRACE_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_EXECUTION_TRACE_PERIOD", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_FLUSH_ON_EXIT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_OUTPUT_DIR", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_PROFILING_UPLOAD_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_PROFILING_URL", Sources: SourceEnvironment, Telemetry: TelemetrySanitizeURL})

	registerBinding(ConsumerBinding{
		ID: "profiler.enablement", Consumer: "profiler.Start",
		Keys: []string{"DD_PROFILING_ENABLED"}, Sampling: SampleProductStart,
	})
	registerBinding(ConsumerBinding{
		ID: "profiler.start", Consumer: "profiler.Start",
		Keys: []string{
			"DD_API_KEY",
			"DD_ENV",
			"DD_PROFILING_AGENTLESS",
			"DD_PROFILING_DEBUG_COMPRESSION_SETTINGS",
			"DD_PROFILING_DELTA",
			"DD_PROFILING_ENDPOINT_COUNT_ENABLED",
			"DD_PROFILING_EXECUTION_TRACE_ENABLED",
			"DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES",
			"DD_PROFILING_EXECUTION_TRACE_PERIOD",
			"DD_PROFILING_FLUSH_ON_EXIT",
			"DD_PROFILING_OUTPUT_DIR",
			"DD_PROFILING_UPLOAD_TIMEOUT",
			"DD_PROFILING_URL",
			"DD_SERVICE",
			"DD_SITE",
			"DD_TAGS",
			"DD_TRACE_STARTUP_LOGS",
			"DD_VERSION",
		},
		Sampling: SampleProductStart, EnvironmentOnly: true,
	})
}

// ProfilerExecutionTraceSnapshot contains profiler execution-trace settings
// sampled at one Start boundary.
type ProfilerExecutionTraceSnapshot struct {
	Enabled bool
	Period  time.Duration
	Limit   int
}

// ProfilerSnapshot contains all Datadog configuration sampled for one
// profiler Start call. Mutable fields are defensive copies.
type ProfilerSnapshot struct {
	AgentURL            *url.URL
	APIKey              string
	Site                string
	Environment         string
	Service             string
	Version             string
	Tags                map[string]string
	Enabled             bool
	Activation          string
	UploadTimeout       time.Duration
	UploadTimeoutError  error
	Agentless           bool
	FlushOnExit         bool
	DeltaProfiles       bool
	StartupLogs         bool
	EndpointCount       bool
	CompressionSettings string
	ExecutionTrace      ProfilerExecutionTraceSnapshot
	ProfilingURL        string
	OutputDir           string
}

// ResolveProfilerSnapshot samples a fresh profiler configuration without
// reporting its events.
func ResolveProfilerSnapshot() (ProfilerSnapshot, []ConfigEvent) {
	snapshot, events, _ := PrepareProfilerSnapshot()
	return snapshot, events
}

// PrepareProfilerSnapshot samples a fresh profiler configuration and returns
// an idempotent reporter for cached git-metadata events. The profiler uses the
// reporter only after publishing its new active runtime state.
func PrepareProfilerSnapshot() (ProfilerSnapshot, []ConfigEvent, func()) {
	p := newEnvironmentProvider()
	var events []ConfigEvent

	resolveString := func(key string) string {
		value, local := resolveStringWithProvider(p, registeredDefinition(key), profilerStartBinding)
		events = append(events, local...)
		return value.Winner.Value
	}
	resolveBool := func(key string, defaultValue bool) bool {
		value, local := resolveBoolWithProvider(
			p,
			registeredDefinition(key),
			profilerStartBinding,
			defaultValue,
		)
		events = append(events, local...)
		return value.Winner.Value
	}
	resolveDuration := func(key string, defaultValue time.Duration) time.Duration {
		value, local := resolveBoundWithProvider(
			p,
			registeredDefinition(key),
			profilerStartBinding,
			defaultValue,
			time.ParseDuration,
		)
		events = append(events, local...)
		logInvalidProfilerDuration(key, value.Attempts, defaultValue)
		return value.Winner.Value
	}
	resolveInt := func(key string, defaultValue int) int {
		value, local := resolveBoundWithProvider(
			p,
			registeredDefinition(key),
			profilerStartBinding,
			defaultValue,
			strconv.Atoi,
		)
		events = append(events, local...)
		logInvalidProfilerInt(key, value.Attempts, defaultValue)
		return value.Winner.Value
	}

	agentURL, local := resolveAgentURLWithProvider(p)
	events = append(events, local...)

	enablement, local := resolveBoundWithProvider(
		newStableProvider(),
		registeredDefinition("DD_PROFILING_ENABLED"),
		profilerEnablementBinding,
		"true",
		func(raw string) (string, error) {
			if raw == "auto" {
				return raw, nil
			}
			value, err := strconv.ParseBool(raw)
			return strconv.FormatBool(value), err
		},
	)
	events = append(events, local...)
	enabled := enablement.Winner.Value != "false"
	activation := "manual"
	if enablement.Winner.Value == "auto" {
		activation = "auto"
	}

	uploadTimeout, local := resolveBoundWithProvider(
		p,
		registeredDefinition("DD_PROFILING_UPLOAD_TIMEOUT"),
		profilerStartBinding,
		defaultProfilerUploadTimeout,
		time.ParseDuration,
	)
	events = append(events, local...)
	var uploadTimeoutError error
	for _, attempt := range uploadTimeout.Attempts {
		if attempt.Present && attempt.Origin == telemetry.OriginEnvVar && attempt.Err != nil {
			uploadTimeoutError = fmt.Errorf("DD_PROFILING_UPLOAD_TIMEOUT: %s", attempt.Err)
		}
	}

	tags := internal.ParseTagString(resolveString("DD_TAGS"))
	internal.CleanGitMetadataTags(tags)
	gitMetadata, reportGitMetadata := PrepareGitMetadataSnapshot()
	maps.Copy(tags, gitMetadata.Tags)

	compression := resolveString("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS")
	if compression == "" {
		compression = "zstd"
	}
	snapshot := ProfilerSnapshot{
		AgentURL:            cloneURL(agentURL),
		APIKey:              resolveString("DD_API_KEY"),
		Site:                resolveString("DD_SITE"),
		Environment:         resolveString("DD_ENV"),
		Service:             resolveString("DD_SERVICE"),
		Version:             resolveString("DD_VERSION"),
		Tags:                maps.Clone(tags),
		Enabled:             enabled,
		Activation:          activation,
		UploadTimeout:       uploadTimeout.Winner.Value,
		UploadTimeoutError:  uploadTimeoutError,
		Agentless:           resolveBool("DD_PROFILING_AGENTLESS", false),
		FlushOnExit:         resolveBool("DD_PROFILING_FLUSH_ON_EXIT", false),
		DeltaProfiles:       resolveBool("DD_PROFILING_DELTA", true),
		StartupLogs:         resolveBool("DD_TRACE_STARTUP_LOGS", true),
		EndpointCount:       resolveBool("DD_PROFILING_ENDPOINT_COUNT_ENABLED", false),
		CompressionSettings: compression,
		ExecutionTrace: ProfilerExecutionTraceSnapshot{
			Enabled: resolveBool(
				"DD_PROFILING_EXECUTION_TRACE_ENABLED",
				runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64",
			),
			Period: resolveDuration("DD_PROFILING_EXECUTION_TRACE_PERIOD", 15*time.Minute),
			Limit:  resolveInt("DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", defaultProfilerExecutionTraceLimit),
		},
		ProfilingURL: resolveString("DD_PROFILING_URL"),
		OutputDir:    resolveString("DD_PROFILING_OUTPUT_DIR"),
	}
	return snapshot, cloneConfigEvents(events), reportGitMetadata
}

// ReportProfilerConfigEvents reports events after profiler runtime publication.
func ReportProfilerConfigEvents(events []ConfigEvent) {
	reportInstrumentationEvents(events)
}

func logInvalidProfilerDuration(key string, attempts []schema.SourceAttempt, defaultValue time.Duration) {
	for _, attempt := range attempts {
		if !attempt.Present || attempt.Origin != telemetry.OriginEnvVar || attempt.Err == nil {
			continue
		}
		log.Warn(
			"Non-duration value for env var %s, defaulting to %d. Parse failed with error: %v",
			key,
			defaultValue,
			attempt.Err.Error(),
		)
	}
}

func logInvalidProfilerInt(key string, attempts []schema.SourceAttempt, defaultValue int) {
	for _, attempt := range attempts {
		if !attempt.Present || attempt.Origin != telemetry.OriginEnvVar || attempt.Err == nil {
			continue
		}
		log.Warn(
			"Non-integer value for env var %s, defaulting to %d. Parse failed with error: %v",
			key,
			defaultValue,
			attempt.Err.Error(),
		)
	}
}
