// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace/configbridge"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	// AppSecDefaultAPISecuritySampleRate is the default API Security request
	// sample rate.
	AppSecDefaultAPISecuritySampleRate = 0.1
	// AppSecDefaultAPISecuritySampleInterval is the default delay between API
	// Security samples.
	AppSecDefaultAPISecuritySampleInterval = 30 * time.Second
	// AppSecDefaultAPISecurityProxySampleRate is the default number of proxy
	// schemas sampled per minute.
	AppSecDefaultAPISecurityProxySampleRate = 300
	// AppSecDefaultDownstreamRequestBodyAnalysisSampleRate is the default
	// downstream body analysis sample rate.
	AppSecDefaultDownstreamRequestBodyAnalysisSampleRate = 0.5
	// AppSecDefaultMaxDownstreamRequestBodyAnalysis is the default maximum
	// number of downstream bodies analyzed for one incoming request.
	AppSecDefaultMaxDownstreamRequestBodyAnalysis = 1
	// AppSecDefaultObfuscatorKeyRegex is the default WAF key obfuscator.
	AppSecDefaultObfuscatorKeyRegex = `(?i)pass|pw(?:or)?d|secret|(?:api|private|public|access)[_-]?key|token|consumer[_-]?(?:id|key|secret)|sign(?:ed|ature)|bearer|authorization|jsessionid|phpsessid|asp\.net[_-]sessionid|sid|jwt`
	// AppSecDefaultObfuscatorValueRegex is the default WAF value obfuscator.
	AppSecDefaultObfuscatorValueRegex = `(?i)(?:p(?:ass)?w(?:or)?d|pass(?:[_-]?phrase)?|secret(?:[_-]?key)?|(?:(?:api|private|public|access)[_-]?)key(?:[_-]?id)?|(?:(?:auth|access|id|refresh)[_-]?)?token|consumer[_-]?(?:id|key|secret)|sign(?:ed|ature)?|auth(?:entication|orization)?|jsessionid|phpsessid|asp\.net(?:[_-]|-)sessionid|sid|jwt)(?:\s*=([^;&]+)|"\s*:\s*("[^"]+"|\d+))|bearer\s+([a-z0-9\._\-]+)|token\s*:\s*([a-z0-9]{13})|gh[opsu]_([0-9a-zA-Z]{36})|ey[I-L][\w=-]+\.(ey[I-L][\w=-]+(?:\.[\w.+\/=-]+)?)|[\-]{5}BEGIN[a-z\s]+PRIVATE\sKEY[\-]{5}([^\-]+)[\-]{5}END[a-z\s]+PRIVATE\sKEY|ssh-rsa\s*([a-z0-9\/\.+]{100,})`
	// AppSecDefaultWAFTimeout is the default time limit for one WAF run.
	AppSecDefaultWAFTimeout = 2 * time.Millisecond
	// AppSecDefaultTraceRate is the default AppSec trace rate limit.
	AppSecDefaultTraceRate int64 = 100
)

var (
	appSecEnablementBinding = ConsumerBinding{
		ID: "appsec.enablement", Consumer: "internal/appsec/config.IsEnabledByEnvironment",
		Keys: []string{"DD_APPSEC_ENABLED"}, Sampling: SampleProductStart,
	}
	appSecSnapshotBinding = ConsumerBinding{
		ID: "appsec.snapshot", Consumer: "internal/appsec/config.NewConfig",
		Keys: []string{
			"DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE",
			"DD_API_SECURITY_ENABLED",
			"DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS",
			"DD_API_SECURITY_REQUEST_SAMPLE_RATE",
			"DD_APM_TRACING_ENABLED",
			"DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP",
			"DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP",
			"DD_APPSEC_RASP_ENABLED",
			"DD_APPSEC_RULES",
			"DD_APPSEC_TRACE_RATE_LIMIT",
			"DD_APPSEC_WAF_TIMEOUT",
		},
		Sampling: SampleProductStart, EnvironmentOnly: true,
	}
	appSecSamplerBinding = ConsumerBinding{
		ID: "appsec.api-security-sampler", Consumer: "internal/appsec/config.NewAPISecConfig",
		Keys: []string{
			"DD_API_SECURITY_PROXY_SAMPLE_RATE",
			"DD_API_SECURITY_SAMPLE_DELAY",
		},
		Sampling: SampleConstructor, EnvironmentOnly: true,
	}
	appSecStackTraceBinding = ConsumerBinding{
		ID: "appsec.stacktrace-init", Consumer: "internal/stacktrace package initialization",
		Keys: []string{
			"DD_APPSEC_MAX_STACK_TRACE_DEPTH",
			"DD_APPSEC_STACK_TRACE_ENABLED",
		},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	}
	appSecBlockedTemplatesBinding = ConsumerBinding{
		ID: "appsec.blocked-templates-init", Consumer: "instrumentation/appsec block action package initialization",
		Keys: []string{
			"DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML",
			"DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON",
		},
		Sampling: SamplePackageInit, EnvironmentOnly: true,
	}
	appSecClientIPBinding = ConsumerBinding{
		ID: "appsec.client-ip-init", Consumer: "internal/appsec/listener/httpsec package initialization",
		Keys: []string{"DD_TRACE_CLIENT_IP_HEADER"}, Sampling: SamplePackageInit, EnvironmentOnly: true,
	}
	appSecSCATelemetryBinding = ConsumerBinding{
		ID: "appsec.sca-init-telemetry", Consumer: "internal/appsec/config package initialization",
		Keys: []string{"DD_APPSEC_SCA_ENABLED"}, Sampling: SamplePackageInit,
	}
	appSecAgenticTelemetryBinding = ConsumerBinding{
		ID: "appsec.agentic-init-telemetry", Consumer: "internal/appsec/config package initialization",
		Keys: []string{"DD_APPSEC_AGENTIC_ONBOARDING"}, Sampling: SamplePackageInit,
	}
)

func init() {
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_PROXY_SAMPLE_RATE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_REQUEST_SAMPLE_RATE", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_API_SECURITY_SAMPLE_DELAY", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_AGENTIC_ONBOARDING", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APPSEC_MAX_STACK_TRACE_DEPTH", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APPSEC_RASP_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_RULES", Sources: SourceEnvironment, Telemetry: TelemetryOmit})
	registerRaw(RawDefinition{Key: "DD_APPSEC_SCA_ENABLED", Sources: SourceStable, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_STACK_TRACE_ENABLED", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_TRACE_RATE_LIMIT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_APPSEC_WAF_TIMEOUT", Sources: SourceEnvironment, Telemetry: TelemetryReport})
	registerRaw(RawDefinition{Key: "DD_TRACE_CLIENT_IP_HEADER", Sources: SourceEnvironment, Telemetry: TelemetryOmit})

	registerBinding(ConsumerBinding{ID: "appsec.enablement", Consumer: "internal/appsec/config.IsEnabledByEnvironment", Keys: []string{"DD_APPSEC_ENABLED"}, Sampling: SampleProductStart})
	registerBinding(ConsumerBinding{ID: "appsec.snapshot", Consumer: "internal/appsec/config.NewConfig", Keys: []string{"DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE", "DD_API_SECURITY_ENABLED", "DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS", "DD_API_SECURITY_REQUEST_SAMPLE_RATE", "DD_APM_TRACING_ENABLED", "DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP", "DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP", "DD_APPSEC_RASP_ENABLED", "DD_APPSEC_RULES", "DD_APPSEC_TRACE_RATE_LIMIT", "DD_APPSEC_WAF_TIMEOUT"}, Sampling: SampleProductStart, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "appsec.api-security-sampler", Consumer: "internal/appsec/config.NewAPISecConfig", Keys: []string{"DD_API_SECURITY_PROXY_SAMPLE_RATE", "DD_API_SECURITY_SAMPLE_DELAY"}, Sampling: SampleConstructor, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "appsec.stacktrace-init", Consumer: "internal/stacktrace package initialization", Keys: []string{"DD_APPSEC_MAX_STACK_TRACE_DEPTH", "DD_APPSEC_STACK_TRACE_ENABLED"}, Sampling: SamplePackageInit, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "appsec.blocked-templates-init", Consumer: "instrumentation/appsec block action package initialization", Keys: []string{"DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML", "DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON"}, Sampling: SamplePackageInit, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "appsec.client-ip-init", Consumer: "internal/appsec/listener/httpsec package initialization", Keys: []string{"DD_TRACE_CLIENT_IP_HEADER"}, Sampling: SamplePackageInit, EnvironmentOnly: true})
	registerBinding(ConsumerBinding{ID: "appsec.sca-init-telemetry", Consumer: "internal/appsec/config package initialization", Keys: []string{"DD_APPSEC_SCA_ENABLED"}, Sampling: SamplePackageInit})
	registerBinding(ConsumerBinding{ID: "appsec.agentic-init-telemetry", Consumer: "internal/appsec/config package initialization", Keys: []string{"DD_APPSEC_AGENTIC_ONBOARDING"}, Sampling: SamplePackageInit})
}

// Diagnostic is the immutable, telemetry-safe event stream emitted while
// resolving an AppSec snapshot.
type Diagnostic = ConfigEvent

// AppSecSnapshot is a constructor-scoped view of AppSec and API Security
// configuration.
type AppSecSnapshot struct {
	APISecurityEnabled                      bool
	APISecuritySampleRate                   float64
	DownstreamRequestBodyAnalysisSampleRate float64
	MaxDownstreamRequestBodyAnalysis        int
	ObfuscatorKeyRegex                      string
	ObfuscatorValueRegex                    string
	RASPEnabled                             bool
	Rules                                   []byte
	RulesPresent                            bool
	RulesError                              error
	TraceRateLimit                          int64
	TracingEnabled                          bool
	TracingEnabledPresent                   bool
	TracingEnabledOrigin                    Origin
	WAFTimeout                              time.Duration
	Origins                                 map[string]Origin
}

// AppSecAPISecuritySnapshot contains the environment values read by the
// legacy API Security constructor.
type AppSecAPISecuritySnapshot struct {
	Enabled                                 bool
	SampleRate                              float64
	DownstreamRequestBodyAnalysisSampleRate float64
	MaxDownstreamRequestBodyAnalysis        int
	RASPEnabled                             bool
	Origins                                 map[string]Origin
}

// AppSecObfuscatorSnapshot contains the two WAF obfuscator expressions.
type AppSecObfuscatorSnapshot struct {
	KeyRegex   string
	ValueRegex string
	Origins    map[string]Origin
}

// ResolveAppSecSnapshot resolves a fresh product-start snapshot without
// retaining rule paths or reporting its returned events.
func ResolveAppSecSnapshot() (AppSecSnapshot, []Diagnostic) {
	var events []ConfigEvent
	origins := make(map[string]Origin)
	apiSecurity, apiSecurityEvents := ResolveAppSecAPISecuritySnapshot()
	obfuscator, obfuscatorEvents := ResolveAppSecObfuscatorSnapshot()
	rules, rulesPresent, rulesError, rulesOrigin, rulesEvents := ResolveAppSecRules()
	traceRate, traceRateOrigin, traceRateEvents := ResolveAppSecTraceRateLimit()
	tracingEnabled, tracingPresent, tracingOrigin, tracingEvents := ResolveAppSecTracingEnabled()
	wafTimeout, wafTimeoutOrigin, wafTimeoutEvents := ResolveAppSecWAFTimeout()
	events = append(events, apiSecurityEvents...)
	events = append(events, obfuscatorEvents...)
	events = append(events, rulesEvents...)
	events = append(events, traceRateEvents...)
	events = append(events, tracingEvents...)
	events = append(events, wafTimeoutEvents...)
	for key, origin := range apiSecurity.Origins {
		origins[key] = origin
	}
	for key, origin := range obfuscator.Origins {
		origins[key] = origin
	}
	origins["DD_APPSEC_RULES"] = rulesOrigin
	origins["DD_APPSEC_TRACE_RATE_LIMIT"] = traceRateOrigin
	origins["DD_APM_TRACING_ENABLED"] = tracingOrigin
	origins["DD_APPSEC_WAF_TIMEOUT"] = wafTimeoutOrigin

	return AppSecSnapshot{
		APISecurityEnabled:                      apiSecurity.Enabled,
		APISecuritySampleRate:                   apiSecurity.SampleRate,
		DownstreamRequestBodyAnalysisSampleRate: apiSecurity.DownstreamRequestBodyAnalysisSampleRate,
		MaxDownstreamRequestBodyAnalysis:        apiSecurity.MaxDownstreamRequestBodyAnalysis,
		ObfuscatorKeyRegex:                      obfuscator.KeyRegex,
		ObfuscatorValueRegex:                    obfuscator.ValueRegex,
		RASPEnabled:                             apiSecurity.RASPEnabled,
		Rules:                                   cloneAppSecBytes(rules),
		RulesPresent:                            rulesPresent,
		RulesError:                              rulesError,
		TraceRateLimit:                          traceRate,
		TracingEnabled:                          tracingEnabled,
		TracingEnabledPresent:                   tracingPresent,
		TracingEnabledOrigin:                    tracingOrigin,
		WAFTimeout:                              wafTimeout,
		Origins:                                 origins,
	}, events
}

// ResolveAppSecAPISecuritySnapshot resolves only the values read by the API
// Security constructor before sampler selection.
func ResolveAppSecAPISecuritySnapshot() (AppSecAPISecuritySnapshot, []Diagnostic) {
	p := newEnvironmentProvider()
	var events []ConfigEvent
	origins := make(map[string]Origin)
	resolveBoolValue := func(key string, defaultValue bool) schema.Resolved[bool] {
		resolved, local := resolveBoolWithProvider(p, registeredDefinition(key), appSecSnapshotBinding, defaultValue)
		events = append(events, local...)
		origins[key] = resolved.Winner.Origin
		return resolved
	}
	resolveValue := func(key string, defaultValue any, parse schema.Parser[any]) schema.Resolved[any] {
		resolved, local := resolveBoundWithProvider(p, registeredDefinition(key), appSecSnapshotBinding, defaultValue, parse)
		events = append(events, local...)
		origins[key] = resolved.Winner.Origin
		return resolved
	}
	enabled := resolveBoolValue("DD_API_SECURITY_ENABLED", true)
	downstreamRate := resolveValue("DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE", AppSecDefaultDownstreamRequestBodyAnalysisSampleRate, func(raw string) (any, error) {
		return strconv.ParseFloat(raw, 64)
	})
	logGenericFloatErrors(downstreamRate, "DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE", AppSecDefaultDownstreamRequestBodyAnalysisSampleRate)
	maxDownstream := resolveValue("DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS", AppSecDefaultMaxDownstreamRequestBodyAnalysis, func(raw string) (any, error) {
		return strconv.Atoi(raw)
	})
	logGenericIntErrors(maxDownstream, "DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS", AppSecDefaultMaxDownstreamRequestBodyAnalysis)
	sampleRate := resolveValue("DD_API_SECURITY_REQUEST_SAMPLE_RATE", AppSecDefaultAPISecuritySampleRate, func(raw string) (any, error) {
		return parseAPISecuritySampleRate(raw)
	})
	logAppSecParseErrors(sampleRate, "DD_API_SECURITY_REQUEST_SAMPLE_RATE", AppSecDefaultAPISecuritySampleRate)
	raspEnabled := resolveBoolValue("DD_APPSEC_RASP_ENABLED", true)

	return AppSecAPISecuritySnapshot{
		Enabled:                                 enabled.Winner.Value,
		SampleRate:                              sampleRate.Winner.Value.(float64),
		DownstreamRequestBodyAnalysisSampleRate: downstreamRate.Winner.Value.(float64),
		MaxDownstreamRequestBodyAnalysis:        maxDownstream.Winner.Value.(int),
		RASPEnabled:                             raspEnabled.Winner.Value,
		Origins:                                 origins,
	}, events
}

// ResolveAPISecuritySampleRate resolves only the deprecated API Security
// request sample rate.
func ResolveAPISecuritySampleRate() (float64, []Diagnostic) {
	resolved, events := resolveBoundWithProvider(
		newEnvironmentProvider(),
		registeredDefinition("DD_API_SECURITY_REQUEST_SAMPLE_RATE"),
		appSecSnapshotBinding,
		any(AppSecDefaultAPISecuritySampleRate),
		func(raw string) (any, error) { return parseAPISecuritySampleRate(raw) },
	)
	logAppSecParseErrors(resolved, "DD_API_SECURITY_REQUEST_SAMPLE_RATE", AppSecDefaultAPISecuritySampleRate)
	return resolved.Winner.Value.(float64), events
}

// ResolveAppSecRASPEnabled resolves only the RASP enablement setting.
func ResolveAppSecRASPEnabled() (bool, Origin, []Diagnostic) {
	resolved, events := resolveBoolWithProvider(
		newEnvironmentProvider(),
		registeredDefinition("DD_APPSEC_RASP_ENABLED"),
		appSecSnapshotBinding,
		true,
	)
	return resolved.Winner.Value, resolved.Winner.Origin, events
}

// ResolveAppSecObfuscatorSnapshot resolves only the WAF obfuscator settings.
func ResolveAppSecObfuscatorSnapshot() (AppSecObfuscatorSnapshot, []Diagnostic) {
	p := newEnvironmentProvider()
	resolve := func(key, fallback string) (schema.Resolved[any], []ConfigEvent) {
		resolved, events := resolveBoundWithProvider(
			p,
			registeredDefinition(key),
			appSecSnapshotBinding,
			any(fallback),
			func(raw string) (any, error) { return parseAppSecRegexp(raw) },
		)
		logAppSecRegexpResolution(resolved, key, fallback)
		return resolved, events
	}
	key, keyEvents := resolve("DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP", AppSecDefaultObfuscatorKeyRegex)
	value, valueEvents := resolve("DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP", AppSecDefaultObfuscatorValueRegex)
	return AppSecObfuscatorSnapshot{
		KeyRegex:   key.Winner.Value.(string),
		ValueRegex: value.Winner.Value.(string),
		Origins: map[string]Origin{
			"DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP":   key.Winner.Origin,
			"DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP": value.Winner.Origin,
		},
	}, append(keyEvents, valueEvents...)
}

// ResolveAppSecRules resolves only the local rules file setting.
func ResolveAppSecRules() ([]byte, bool, error, Origin, []Diagnostic) {
	resolved, events := resolveBoundWithProvider(
		newEnvironmentProvider(),
		registeredDefinition("DD_APPSEC_RULES"),
		appSecSnapshotBinding,
		any([]byte(nil)),
		func(raw string) (any, error) { return readAppSecFile(raw, true) },
	)
	rules, _ := resolved.Winner.Value.([]byte)
	return cloneAppSecBytes(rules),
		sourcePresent(resolved.Attempts),
		unresolvedError(resolved.Winner.DefaultUsed, resolved.Attempts),
		resolved.Winner.Origin,
		events
}

// ResolveAppSecTraceRateLimit resolves only the AppSec trace rate limit.
func ResolveAppSecTraceRateLimit() (int64, Origin, []Diagnostic) {
	resolved, events := resolveBoundWithProvider(
		newEnvironmentProvider(),
		registeredDefinition("DD_APPSEC_TRACE_RATE_LIMIT"),
		appSecSnapshotBinding,
		any(AppSecDefaultTraceRate),
		func(raw string) (any, error) { return parseAppSecTraceRate(raw) },
	)
	logAppSecTraceRateErrors(resolved)
	return resolved.Winner.Value.(int64), resolved.Winner.Origin, events
}

// ResolveAppSecWAFTimeout resolves only the WAF timeout.
func ResolveAppSecWAFTimeout() (time.Duration, Origin, []Diagnostic) {
	resolved, events := resolveBoundWithProvider(
		newEnvironmentProvider(),
		registeredDefinition("DD_APPSEC_WAF_TIMEOUT"),
		appSecSnapshotBinding,
		any(AppSecDefaultWAFTimeout),
		func(raw string) (any, error) { return parseAppSecWAFTimeout(raw) },
	)
	logAppSecWAFTimeoutErrors(resolved)
	return resolved.Winner.Value.(time.Duration), resolved.Winner.Origin, events
}

// ResolveAppSecTracingEnabled resolves AppSec's environment-only APM gate.
func ResolveAppSecTracingEnabled() (bool, bool, Origin, []Diagnostic) {
	resolved, events := resolveBoolWithProvider(
		newEnvironmentProvider(),
		registeredDefinition("DD_APM_TRACING_ENABLED"),
		appSecSnapshotBinding,
		true,
	)
	return resolved.Winner.Value, sourcePresent(resolved.Attempts), resolved.Winner.Origin, events
}

// ReportAppSecDiagnostics reports the event stream returned with a snapshot.
func ReportAppSecDiagnostics(events []Diagnostic) {
	reportInstrumentationEvents(events)
}

// ResolveAppSecEnablement preserves the stable-config precedence used by AppSec
// activation. Invalid higher-priority values fall through to a valid lower
// source.
func ResolveAppSecEnablement() (enabled, present bool, origin Origin, err error, events []Diagnostic) {
	resolved, events := resolveAppSecStable(
		"DD_APPSEC_ENABLED",
		appSecEnablementBinding,
		false,
		strconv.ParseBool,
	)
	return resolved.Winner.Value,
		!resolved.Winner.DefaultUsed,
		resolved.Winner.Origin,
		appSecStableBoolError("DD_APPSEC_ENABLED", resolved.Winner.DefaultUsed, resolved.Attempts),
		events
}

// ResolveAPISecuritySamplerConfig resolves only the sampler setting used by the
// selected API Security mode.
func ResolveAPISecuritySamplerConfig(proxy bool) (proxyRate int, interval time.Duration) {
	p := newEnvironmentProvider()
	proxyRate = AppSecDefaultAPISecurityProxySampleRate
	interval = AppSecDefaultAPISecuritySampleInterval
	if proxy {
		resolved, events := resolveBoundWithProvider(
			p,
			registeredDefinition("DD_API_SECURITY_PROXY_SAMPLE_RATE"),
			appSecSamplerBinding,
			proxyRate,
			strconv.Atoi,
		)
		logGenericIntErrors(resolved, "DD_API_SECURITY_PROXY_SAMPLE_RATE", proxyRate)
		reportInstrumentationEvents(events)
		return resolved.Winner.Value, interval
	}
	resolved, events := resolveBoundWithProvider(
		p,
		registeredDefinition("DD_API_SECURITY_SAMPLE_DELAY"),
		appSecSamplerBinding,
		interval,
		func(raw string) (time.Duration, error) {
			return time.ParseDuration(raw + "s")
		},
	)
	logGenericDurationErrors(resolved, "DD_API_SECURITY_SAMPLE_DELAY", interval)
	reportInstrumentationEvents(events)
	return proxyRate, resolved.Winner.Value
}

// AppSecPackageInitTelemetry reports AppSec package-init-only configuration.
// The returned error is limited to invalid SCA values.
func ReportAppSecSCAInitTelemetry() error {
	sca, scaEvents := resolveAppSecStable(
		"DD_APPSEC_SCA_ENABLED",
		appSecSCATelemetryBinding,
		false,
		strconv.ParseBool,
	)
	reportInstrumentationEvents(winnerConfigEvents(scaEvents, "DD_APPSEC_SCA_ENABLED", sca.Winner, false))
	return appSecStableBoolError("DD_APPSEC_SCA_ENABLED", sca.Winner.DefaultUsed, sca.Attempts)
}

// ReportAppSecAgenticInitTelemetry reports the agentic-onboarding marker once
// at AppSec config package initialization.
func ReportAppSecAgenticInitTelemetry() {
	agentic, agenticEvents := resolveAppSecStable(
		"DD_APPSEC_AGENTIC_ONBOARDING",
		appSecAgenticTelemetryBinding,
		"",
		func(raw string) (string, error) { return raw, nil },
	)
	reportInstrumentationEvents(winnerConfigEvents(agenticEvents, "DD_APPSEC_AGENTIC_ONBOARDING", agentic.Winner, true))
}

// ResolveAppSecStackTraceConfig resolves the init-scoped stack trace settings.
func ResolveAppSecStackTraceConfig() configbridge.Config {
	snapshot, report := bootstrap.ClaimAppSecStackTraceTelemetry()
	if report {
		reportInstrumentationEvents(appSecStackTraceEvents(snapshot))
	}
	return configbridge.Config{
		Enabled:       snapshot.Enabled,
		MaxDepth:      snapshot.MaxDepth,
		TopFrameDepth: snapshot.TopFrameDepth,
	}
}

// AppSecBlockedTemplates contains package-init copies of the custom blocked
// response bodies.
type AppSecBlockedTemplates struct {
	JSON []byte
	HTML []byte
}

// ResolveAppSecBlockedTemplates resolves and reads custom response templates.
// Paths and file contents are omitted from returned events.
func ResolveAppSecBlockedTemplates(jsonDefault, htmlDefault []byte) (AppSecBlockedTemplates, []Diagnostic) {
	p := newEnvironmentProvider()
	resolve := func(key string, fallback []byte) ([]byte, []ConfigEvent) {
		resolved, events := resolveBoundWithProvider(
			p,
			registeredDefinition(key),
			appSecBlockedTemplatesBinding,
			cloneAppSecBytes(fallback),
			func(raw string) ([]byte, error) {
				return readAppSecFile(raw, false)
			},
		)
		for _, attempt := range resolved.Attempts {
			if attempt.Present && attempt.Err != nil {
				log.Error("Could not read template configured by %s: %v", key, attempt.Err.Error())
			}
		}
		return cloneAppSecBytes(resolved.Winner.Value), events
	}
	jsonTemplate, jsonEvents := resolve("DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON", jsonDefault)
	htmlTemplate, htmlEvents := resolve("DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML", htmlDefault)
	return AppSecBlockedTemplates{JSON: jsonTemplate, HTML: htmlTemplate}, append(jsonEvents, htmlEvents...)
}

// AppSecClientIPHeader resolves the package-init client-IP header override.
func AppSecClientIPHeader() string {
	resolved, events := resolveString("DD_TRACE_CLIENT_IP_HEADER", appSecClientIPBinding)
	reportInstrumentationEvents(events)
	return resolved.Winner.Value
}

func installAppSecInitProviders() {
	ResolveAppSecStackTraceConfig()
}

func appSecStackTraceEvents(snapshot bootstrap.AppSecStackTraceSnapshot) []ConfigEvent {
	type setting struct {
		key          string
		raw          string
		present      bool
		err          error
		defaultValue any
	}
	settings := []setting{
		{
			key:          "DD_APPSEC_STACK_TRACE_ENABLED",
			raw:          snapshot.EnabledRaw,
			present:      snapshot.EnabledPresent,
			err:          snapshot.EnabledError,
			defaultValue: true,
		},
		{
			key:          "DD_APPSEC_MAX_STACK_TRACE_DEPTH",
			raw:          snapshot.DepthRaw,
			present:      snapshot.DepthPresent,
			err:          snapshot.DepthError,
			defaultValue: 32,
		},
	}
	events := make([]ConfigEvent, 0, len(settings)*2)
	for _, setting := range settings {
		def := registeredDefinition(setting.key)
		events = append(events,
			ConfigEvent{
				Kind:          EventConfiguration,
				BindingID:     appSecStackTraceBinding.ID,
				Name:          setting.key,
				Value:         setting.raw,
				Present:       setting.present,
				Valid:         setting.present && setting.err == nil,
				Err:           setting.err,
				Origin:        telemetry.OriginEnvVar,
				SourceOrdinal: schema.SourceOrdinalEnvironment,
				Policy:        def.Telemetry,
				Cadence:       ReportOncePerGeneration,
				ReportValue:   setting.present,
			},
			ConfigEvent{
				Kind:          EventConfiguration,
				BindingID:     appSecStackTraceBinding.ID,
				Name:          setting.key,
				Value:         setting.defaultValue,
				Present:       true,
				Valid:         true,
				Origin:        telemetry.OriginDefault,
				SourceOrdinal: schema.SourceOrdinalDefault,
				Policy:        def.Telemetry,
				Cadence:       ReportOncePerGeneration,
				ReportValue:   true,
			},
		)
	}
	return events
}

func parseAPISecuritySampleRate(raw string) (float64, error) {
	if raw == "" {
		return AppSecDefaultAPISecuritySampleRate, nil
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if rate < 0 {
		return 0, nil
	}
	if rate > 1 {
		return 1, nil
	}
	return rate, nil
}

func parseAppSecRegexp(raw string) (string, error) {
	if _, err := regexp.Compile(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func parseAppSecWAFTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return AppSecDefaultWAFTimeout, nil
	}
	value := raw
	if lastRune, _ := utf8.DecodeLastRuneInString(value); !unicode.IsLetter(lastRune) {
		value += "us"
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, errors.New("expecting a strictly positive duration")
	}
	return parsed, nil
}

func parseAppSecTraceRate(raw string) (int64, error) {
	if raw == "" {
		return AppSecDefaultTraceRate, nil
	}
	parsed, err := strconv.ParseUint(raw, 10, 0)
	if err != nil {
		return 0, err
	}
	if parsed == 0 {
		return 0, errors.New("expecting a value strictly greater than 0")
	}
	if parsed > math.MaxInt64 {
		return 0, errors.New("expecting a value less than or equal to math.MaxInt64")
	}
	return int64(parsed), nil
}

func readAppSecFile(path string, emptyIsDefault bool) ([]byte, error) {
	if emptyIsDefault && path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if emptyIsDefault && os.IsNotExist(err) {
			return nil, fmt.Errorf("appsec: could not find the rules file in path %s: %w", path, err)
		}
		return nil, err
	}
	return cloneAppSecBytes(data), nil
}

func cloneAppSecBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func unresolvedError(defaultUsed bool, attempts []schema.SourceAttempt) error {
	if !defaultUsed {
		return nil
	}
	var err error
	for _, attempt := range attempts {
		if attempt.Present && attempt.Err != nil {
			err = errors.Join(err, attempt.Err)
		}
	}
	return err
}

func appSecStableBoolError(key string, defaultUsed bool, attempts []schema.SourceAttempt) error {
	if !defaultUsed {
		return nil
	}
	var err error
	for i := len(attempts) - 1; i >= 0; i-- {
		attempt := attempts[i]
		if attempt.Present && attempt.Err != nil {
			err = errors.Join(err, fmt.Errorf(
				"non-boolean value for %s: '%s' in %s configuration, dropping",
				key,
				attempt.Raw,
				attempt.Origin,
			))
		}
	}
	return err
}

func resolveAppSecStable[T any](
	key string,
	binding ConsumerBinding,
	defaultValue T,
	parse schema.Parser[T],
) (schema.Resolved[T], []ConfigEvent) {
	resolved, events := resolveBound(
		registeredDefinition(key),
		binding,
		defaultValue,
		parse,
	)
	return filterAppSecStableEmpty(resolved, events, key, defaultValue, parse)
}

func filterAppSecStableEmpty[T any](
	resolved schema.Resolved[T],
	events []ConfigEvent,
	key string,
	defaultValue T,
	parse schema.Parser[T],
) (schema.Resolved[T], []ConfigEvent) {
	attempts := make([]schema.SourceAttempt, 0, len(resolved.Attempts))
	skippedOrigins := make(map[Origin]struct{}, 2)
	winner := schema.Winner[T]{
		Value:       defaultValue,
		Origin:      telemetry.OriginDefault,
		DefaultUsed: true,
	}
	for _, attempt := range resolved.Attempts {
		if appSecEmptyStableAttempt(attempt) {
			skippedOrigins[attempt.Origin] = struct{}{}
			continue
		}
		attempts = append(attempts, attempt)
		if !attempt.Present || !attempt.Valid {
			continue
		}
		value, err := parse(attempt.Raw)
		if err == nil {
			winner = schema.Winner[T]{
				Value:    value,
				Origin:   attempt.Origin,
				ConfigID: attempt.ConfigID,
			}
		}
	}
	resolved.Winner = winner
	resolved.Attempts = attempts

	filteredEvents := make([]ConfigEvent, 0, len(events))
	for _, event := range events {
		_, skipped := skippedOrigins[event.Origin]
		if skipped && event.Kind == EventConfiguration && event.Name == key {
			continue
		}
		filteredEvents = append(filteredEvents, event)
	}
	return resolved, filteredEvents
}

func appSecEmptyStableAttempt(attempt schema.SourceAttempt) bool {
	if !attempt.Present || attempt.Raw != "" {
		return false
	}
	return attempt.Origin == telemetry.OriginManagedStableConfig ||
		attempt.Origin == telemetry.OriginLocalStableConfig
}

func winnerConfigEvents[T any](events []ConfigEvent, key string, winner schema.Winner[T], includeDefault bool) []ConfigEvent {
	for _, event := range events {
		if event.Name != key || event.Kind != EventConfiguration {
			continue
		}
		if winner.DefaultUsed {
			if includeDefault && event.Origin == telemetry.OriginDefault {
				event.Value = winner.Value
				return []ConfigEvent{event}
			}
			continue
		}
		if event.Present && event.Valid && event.Origin == winner.Origin && event.ConfigID == winner.ConfigID {
			event.Value = winner.Value
			return []ConfigEvent{event}
		}
	}
	return nil
}

func logAppSecParseErrors[T any](resolved schema.Resolved[T], key string, defaultValue any) {
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil {
			log.Debug("appsec: could not parse the env var %s=%s as a duration: %v. Using default value %v.", key, attempt.Raw, attempt.Err, defaultValue)
		}
	}
}

func logGenericFloatErrors[T any](resolved schema.Resolved[T], key string, defaultValue float64) {
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil {
			log.Warn("Non-float value for env var %s, defaulting to %f. Parse failed with error: %v", key, defaultValue, attempt.Err.Error())
		}
	}
}

func logGenericIntErrors[T any](resolved schema.Resolved[T], key string, defaultValue int) {
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil {
			log.Warn("Non-integer value for env var %s, defaulting to %d. Parse failed with error: %v", key, defaultValue, attempt.Err.Error())
		}
	}
}

func logGenericDurationErrors[T any](resolved schema.Resolved[T], key string, defaultValue time.Duration) {
	for _, attempt := range resolved.Attempts {
		if attempt.Present && attempt.Err != nil {
			log.Warn("Non-duration value for env var %s, defaulting to %d. Parse failed with error: %v", key, defaultValue, attempt.Err.Error())
		}
	}
}

func logAppSecRegexpResolution(resolved schema.Resolved[any], key, defaultValue string) {
	found := false
	for _, attempt := range resolved.Attempts {
		if !attempt.Present {
			continue
		}
		found = true
		if attempt.Err != nil {
			log.Debug("appsec: unexpected configuration value of %s=%v: could not compile the configured obfuscator regular expression. Using default value %v.", key, attempt.Raw, defaultValue)
		} else {
			log.Debug("appsec: starting with the configured obfuscator regular expression %s", key)
		}
	}
	if !found {
		log.Debug("appsec: %s not defined, starting with the default obfuscator regular expression", key)
	}
}

func logAppSecTraceRateErrors(resolved schema.Resolved[any]) {
	for _, attempt := range resolved.Attempts {
		if !attempt.Present || attempt.Err == nil {
			continue
		}
		parsed, err := strconv.ParseUint(attempt.Raw, 10, 0)
		switch {
		case err != nil:
			log.Debug("appsec: could not parse the env var DD_APPSEC_TRACE_RATE_LIMIT=%s as a duration: %v. Using default value %v.", attempt.Raw, err, AppSecDefaultTraceRate)
		case parsed == 0:
			log.Debug("appsec: unexpected configuration value of DD_APPSEC_TRACE_RATE_LIMIT=%v: expecting a value strictly greater than 0. Using default value %v.", parsed, AppSecDefaultTraceRate)
		case parsed > math.MaxInt64:
			log.Debug("appsec: unexpected configuration value of DD_APPSEC_TRACE_RATE_LIMIT=%v: expecting a value less than or equal to math.MaxInt64. Using default value %v.", parsed, AppSecDefaultTraceRate)
		}
	}
}

func logAppSecWAFTimeoutErrors(resolved schema.Resolved[any]) {
	for _, attempt := range resolved.Attempts {
		if !attempt.Present || attempt.Err == nil {
			continue
		}
		value := attempt.Raw
		if lastRune, _ := utf8.DecodeLastRuneInString(value); !unicode.IsLetter(lastRune) {
			value += "us"
		}
		parsed, err := time.ParseDuration(value)
		if err != nil {
			log.Debug("appsec: could not parse the env var DD_APPSEC_WAF_TIMEOUT=%s as a duration: %v. Using default value %v.", value, err, AppSecDefaultWAFTimeout)
			continue
		}
		log.Debug("appsec: unexpected configuration value of DD_APPSEC_WAF_TIMEOUT=%v: expecting a strictly positive duration. Using default value %v.", parsed, AppSecDefaultWAFTimeout)
	}
}
