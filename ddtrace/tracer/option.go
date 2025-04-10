// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"net/http"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

var (
	// defaultMaxTagsHeaderLen specifies the default maximum length of the X-Datadog-Tags header value.
	defaultMaxTagsHeaderLen = 128
)

// StartOption represents a function that can be provided as a parameter to Start.
type StartOption = v2.StartOption

// MarkIntegrationImported labels the given integration as imported
func MarkIntegrationImported(integration string) bool {
	return v2.MarkIntegrationImported(integration)
}

// WithAppSecEnabled specifies whether AppSec features should be activated
// or not.
//
// By default, AppSec features are enabled if `DD_APPSEC_ENABLED` is set to a
// truthy value; and may be enabled by remote configuration if
// `DD_APPSEC_ENABLED` is not set at all.
//
// Using this option to explicitly disable appsec also prevents it from being
// remote activated.
func WithAppSecEnabled(enabled bool) StartOption {
	return v2.WithAppSecEnabled(enabled)
}

// WithFeatureFlags specifies a set of feature flags to enable. Please take into account
// that most, if not all features flags are considered to be experimental and result in
// unexpected bugs.
func WithFeatureFlags(feats ...string) StartOption {
	return v2.WithFeatureFlags(feats...)
}

// WithLogger sets logger as the tracer's error printer.
// Diagnostic and startup tracer logs are prefixed to simplify the search within logs.
// If JSON logging format is required, it's possible to wrap tracer logs using an existing JSON logger with this
// function. To learn more about this possibility, please visit: https://github.com/DataDog/dd-trace-go/issues/2152#issuecomment-1790586933
func WithLogger(logger ddtrace.Logger) StartOption {
	return v2.WithLogger(logger)
}

// WithPrioritySampling is deprecated, and priority sampling is enabled by default.
// When using distributed tracing, the priority sampling value is propagated in order to
// get all the parts of a distributed trace sampled.
// To learn more about priority sampling, please visit:
// https://docs.datadoghq.com/tracing/getting_further/trace_sampling_and_storage/#priority-sampling-for-distributed-tracing
func WithPrioritySampling() StartOption {
	return nil
}

// WithDebugStack can be used to globally enable or disable the collection of stack traces when
// spans finish with errors. It is enabled by default. This is a global version of the NoDebugStack
// FinishOption.
func WithDebugStack(enabled bool) StartOption {
	return v2.WithDebugStack(enabled)
}

// WithDebugMode enables debug mode on the tracer, resulting in more verbose logging.
func WithDebugMode(enabled bool) StartOption {
	return v2.WithDebugMode(enabled)
}

// WithLambdaMode enables lambda mode on the tracer, for use with AWS Lambda.
// This option is only required if the the Datadog Lambda Extension is not
// running.
func WithLambdaMode(enabled bool) StartOption {
	return v2.WithLambdaMode(enabled)
}

// WithSendRetries enables re-sending payloads that are not successfully
// submitted to the agent.  This will cause the tracer to retry the send at
// most `retries` times.
func WithSendRetries(retries int) StartOption {
	return v2.WithSendRetries(retries)
}

// WithRetryInterval sets the interval, in seconds, for retrying submitting payloads to the agent.
func WithRetryInterval(interval int) StartOption {
	return v2.WithRetryInterval(interval)
}

// WithPropagator sets an alternative propagator to be used by the tracer.
func WithPropagator(p Propagator) StartOption {
	return v2.WithPropagator(&propagatorV1Adapter{propagator: p})
}

// WithServiceName is deprecated. Please use WithService.
// If you are using an older version and you are upgrading from WithServiceName
// to WithService, please note that WithService will determine the service name of
// server and framework integrations.
func WithServiceName(name string) StartOption {
	return v2.WithService(name)
}

// WithService sets the default service name for the program.
func WithService(name string) StartOption {
	return v2.WithService(name)
}

// WithGlobalServiceName causes contrib libraries to use the global service name and not any locally defined service name.
// This is synonymous with `DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED`.
func WithGlobalServiceName(enabled bool) StartOption {
	return v2.WithGlobalServiceName(enabled)
}

// WithAgentAddr sets the address where the agent is located. The default is
// localhost:8126. It should contain both host and port.
func WithAgentAddr(addr string) StartOption {
	return v2.WithAgentAddr(addr)
}

// WithAgentTimeout sets the timeout for the agent connection. Timeout is in seconds.
func WithAgentTimeout(timeout int) StartOption {
	return v2.WithAgentTimeout(timeout)
}

// WithEnv sets the environment to which all traces started by the tracer will be submitted.
// The default value is the environment variable DD_ENV, if it is set.
func WithEnv(env string) StartOption {
	return v2.WithEnv(env)
}

// WithServiceMapping determines service "from" to be renamed to service "to".
// This option is is case sensitive and can be used multiple times.
func WithServiceMapping(from, to string) StartOption {
	return v2.WithServiceMapping(from, to)
}

// WithPeerServiceDefaults sets default calculation for peer.service.
// Related documentation: https://docs.datadoghq.com/tracing/guide/inferred-service-opt-in/?tab=go#apm-tracer-configuration
func WithPeerServiceDefaults(enabled bool) StartOption {
	return v2.WithPeerServiceDefaults(enabled)
}

// WithPeerServiceMapping determines the value of the peer.service tag "from" to be renamed to service "to".
func WithPeerServiceMapping(from, to string) StartOption {
	return v2.WithPeerServiceMapping(from, to)
}

// WithGlobalTag sets a key/value pair which will be set as a tag on all spans
// created by tracer. This option may be used multiple times.
func WithGlobalTag(k string, v interface{}) StartOption {
	return v2.WithGlobalTag(k, v)
}

type samplerV1Adapter struct {
	sampler Sampler
}

// Sample implements tracer.Sampler.
func (sa *samplerV1Adapter) Sample(span *v2.Span) bool {
	s := internal.WrapSpan(span)
	return sa.sampler.Sample(s)
}

// WithSampler sets the given sampler to be used with the tracer. By default
// an all-permissive sampler is used.
func WithSampler(s Sampler) StartOption {
	return v2.WithSampler(&samplerV1Adapter{sampler: s})
}

const (
	defaultHTTPTimeout = 2 * time.Second // defines the current timeout before giving up with the send process
)

// WithHTTPRoundTripper is deprecated. Please consider using WithHTTPClient instead.
// The function allows customizing the underlying HTTP transport for emitting spans.
func WithHTTPRoundTripper(r http.RoundTripper) StartOption {
	return WithHTTPClient(&http.Client{
		Transport: r,
		Timeout:   defaultHTTPTimeout,
	})
}

// WithHTTPClient specifies the HTTP client to use when emitting spans to the agent.
func WithHTTPClient(client *http.Client) StartOption {
	return v2.WithHTTPClient(client)
}

// WithUDS configures the HTTP client to dial the Datadog Agent via the specified Unix Domain Socket path.
func WithUDS(socketPath string) StartOption {
	return v2.WithUDS(socketPath)
}

// WithAnalytics allows specifying whether Trace Search & Analytics should be enabled
// for integrations.
func WithAnalytics(on bool) StartOption {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the global sampling rate for sampling APM events.
func WithAnalyticsRate(rate float64) StartOption {
	return v2.WithAnalyticsRate(rate)
}

// WithRuntimeMetrics enables automatic collection of runtime metrics every 10 seconds.
func WithRuntimeMetrics() StartOption {
	return v2.WithRuntimeMetrics()
}

// WithDogstatsdAddress specifies the address to connect to for sending metrics to the Datadog
// Agent. It should be a "host:port" string, or the path to a unix domain socket.If not set, it
// attempts to determine the address of the statsd service according to the following rules:
//  1. Look for /var/run/datadog/dsd.socket and use it if present. IF NOT, continue to #2.
//  2. The host is determined by DD_AGENT_HOST, and defaults to "localhost"
//  3. The port is retrieved from the agent. If not present, it is determined by DD_DOGSTATSD_PORT, and defaults to 8125
//
// This option is in effect when WithRuntimeMetrics is enabled.
func WithDogstatsdAddress(addr string) StartOption {
	return v2.WithDogstatsdAddr(addr)
}

// WithSamplingRules specifies the sampling rates to apply to spans based on the
// provided rules.
func WithSamplingRules(rules []SamplingRule) StartOption {
	rr := make([]v2.SamplingRule, len(rules))
	for i, r := range rules {
		var ssr []v2.SamplingRule
		if r.ruleType == SamplingRuleSpan {
			ssr = v2.SpanSamplingRules(v2.Rule{
				Rate: r.Rate,
			})
		} else {
			ssr = v2.TraceSamplingRules(v2.Rule{
				Rate: r.Rate,
			})
		}
		rr[i] = ssr[0]
		rr[i].MaxPerSecond = r.MaxPerSecond
		rr[i].Name = r.Name
		rr[i].Resource = r.Resource
		rr[i].Service = r.Service
		rr[i].Tags = r.Tags
	}
	return v2.WithSamplingRules(rr)
}

// WithServiceVersion specifies the version of the service that is running. This will
// be included in spans from this service in the "version" tag, provided that
// span service name and config service name match. Do NOT use with WithUniversalVersion.
func WithServiceVersion(version string) StartOption {
	return v2.WithServiceVersion(version)
}

// WithUniversalVersion specifies the version of the service that is running, and will be applied to all spans,
// regardless of whether span service name and config service name match.
// See: WithService, WithServiceVersion. Do NOT use with WithServiceVersion.
func WithUniversalVersion(version string) StartOption {
	return v2.WithUniversalVersion(version)
}

// WithHostname allows specifying the hostname with which to mark outgoing traces.
func WithHostname(name string) StartOption {
	return v2.WithHostname(name)
}

// WithTraceEnabled allows specifying whether tracing will be enabled
func WithTraceEnabled(enabled bool) StartOption {
	return v2.WithTraceEnabled(enabled)
}

// WithLogStartup allows enabling or disabling the startup log.
func WithLogStartup(enabled bool) StartOption {
	return v2.WithLogStartup(enabled)
}

// WithProfilerCodeHotspots enables the code hotspots integration between the
// tracer and profiler. This is done by automatically attaching pprof labels
// called "span id" and "local root span id" when new spans are created. You
// should not use these label names in your own code when this is enabled. The
// enabled value defaults to the value of the
// DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED env variable or true.
func WithProfilerCodeHotspots(enabled bool) StartOption {
	return v2.WithProfilerCodeHotspots(enabled)
}

// WithProfilerEndpoints enables the endpoints integration between the tracer
// and profiler. This is done by automatically attaching a pprof label called
// "trace endpoint" holding the resource name of the top-level service span if
// its type is "http", "rpc" or "" (default). You should not use this label
// name in your own code when this is enabled. The enabled value defaults to
// the value of the DD_PROFILING_ENDPOINT_COLLECTION_ENABLED env variable or
// true.
func WithProfilerEndpoints(enabled bool) StartOption {
	return v2.WithProfilerEndpoints(enabled)
}

// WithDebugSpansMode enables debugging old spans that may have been
// abandoned, which may prevent traces from being set to the Datadog
// Agent, especially if partial flushing is off.
// This setting can also be configured by setting DD_TRACE_DEBUG_ABANDONED_SPANS
// to true. The timeout will default to 10 minutes, unless overwritten
// by DD_TRACE_ABANDONED_SPAN_TIMEOUT.
// This feature is disabled by default. Turning on this debug mode may
// be expensive, so it should only be enabled for debugging purposes.
func WithDebugSpansMode(timeout time.Duration) StartOption {
	return v2.WithDebugSpansMode(timeout)
}

// WithPartialFlushing enables flushing of partially finished traces.
// This is done after "numSpans" have finished in a single local trace at
// which point all finished spans in that trace will be flushed, freeing up
// any memory they were consuming. This can also be configured by setting
// DD_TRACE_PARTIAL_FLUSH_ENABLED to true, which will default to 1000 spans
// unless overriden with DD_TRACE_PARTIAL_FLUSH_MIN_SPANS. Partial flushing
// is disabled by default.
func WithPartialFlushing(numSpans int) StartOption {
	return v2.WithPartialFlushing(numSpans)
}

// WithStatsComputation enables client-side stats computation, allowing
// the tracer to compute stats from traces. This can reduce network traffic
// to the Datadog Agent, and produce more accurate stats data.
// This can also be configured by setting DD_TRACE_STATS_COMPUTATION_ENABLED to true.
// Client-side stats is off by default.
func WithStatsComputation(enabled bool) StartOption {
	return v2.WithStatsComputation(enabled)
}

// StartSpanOption is a configuration option for StartSpan. It is aliased in order
// to help godoc group all the functions returning it together. It is considered
// more correct to refer to it as the type as the origin, ddtrace.StartSpanOption.
type StartSpanOption = ddtrace.StartSpanOption

// Tag sets the given key/value pair as a tag on the started Span.
func Tag(k string, v interface{}) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		if cfg.Tags == nil {
			cfg.Tags = make(map[string]interface{})
		}
		if k == ext.SamplingPriority {
			k = "_sampling_priority_v1shim"
		}
		cfg.Tags[k] = v
	}
}

// ServiceName sets the given service name on the started span. For example "http.server".
func ServiceName(name string) StartSpanOption {
	return Tag(ext.ServiceName, name)
}

// ResourceName sets the given resource name on the started span. A resource could
// be an SQL query, a URL, an RPC method or something else.
func ResourceName(name string) StartSpanOption {
	return Tag(ext.ResourceName, name)
}

// SpanType sets the given span type on the started span. Some examples in the case of
// the Datadog APM product could be "web", "db" or "cache".
func SpanType(name string) StartSpanOption {
	return Tag(ext.SpanType, name)
}

// WithSpanLinks sets span links on the started span.
func WithSpanLinks(links []ddtrace.SpanLink) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.SpanLinks = append(cfg.SpanLinks, links...)
	}
}

var measuredTag = Tag(keyMeasured, 1)

// Measured marks this span to be measured for metrics and stats calculations.
func Measured() StartSpanOption {
	// cache a global instance of this tag: saves one alloc/call
	return measuredTag
}

// WithSpanID sets the SpanID on the started span, instead of using a random number.
// If there is no parent Span (eg from ChildOf), then the TraceID will also be set to the
// value given here.
func WithSpanID(id uint64) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.SpanID = id
	}
}

// ChildOf tells StartSpan to use the given span context as a parent for the
// created span.
func ChildOf(ctx ddtrace.SpanContext) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.Parent = ctx
	}
}

// StartTime sets a custom time as the start time for the created span. By
// default a span is started using the creation time.
func StartTime(t time.Time) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.StartTime = t
	}
}

// AnalyticsRate sets a custom analytics rate for a span. It decides the percentage
// of events that will be picked up by the App Analytics product. It's represents a
// float64 between 0 and 1 where 0.5 would represent 50% of events.
func AnalyticsRate(rate float64) StartSpanOption {
	if math.IsNaN(rate) {
		return func(cfg *ddtrace.StartSpanConfig) {}
	}
	return Tag(ext.EventSampleRate, rate)
}

// FinishOption is a configuration option for FinishSpan. It is aliased in order
// to help godoc group all the functions returning it together. It is considered
// more correct to refer to it as the type as the origin, ddtrace.FinishOption.
type FinishOption = ddtrace.FinishOption

// FinishTime sets the given time as the finishing time for the span. By default,
// the current time is used.
func FinishTime(t time.Time) FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.FinishTime = t
	}
}

// WithError marks the span as having had an error. It uses the information from
// err to set tags such as the error message, error type and stack trace. It has
// no effect if the error is nil.
func WithError(err error) FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.Error = err
	}
}

// NoDebugStack prevents any error presented using the WithError finishing option
// from generating a stack trace. This is useful in situations where errors are frequent
// and performance is critical.
func NoDebugStack() FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.NoDebugStack = true
	}
}

// StackFrames limits the number of stack frames included into erroneous spans to n, starting from skip.
func StackFrames(n, skip uint) FinishOption {
	if n == 0 {
		return NoDebugStack()
	}
	return func(cfg *ddtrace.FinishConfig) {
		cfg.StackFrames = n
		cfg.SkipStackFrames = skip
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headerAsTags []string) StartOption {
	return v2.WithHeaderTags(headerAsTags)
}

// UserMonitoringConfig is used to configure what is used to identify a user.
// This configuration can be set by combining one or several UserMonitoringOption with a call to SetUser().
type UserMonitoringConfig = v2.UserMonitoringConfig

// UserMonitoringOption represents a function that can be provided as a parameter to SetUser.
type UserMonitoringOption = v2.UserMonitoringOption

// WithUserMetadata returns the option setting additional metadata of the authenticated user.
// This can be used multiple times and the given data will be tracked as `usr.{key}=value`.
func WithUserMetadata(key, value string) UserMonitoringOption {
	return v2.WithUserMetadata(key, value)
}

// WithUserLogin returns the option setting the login of the authenticated user.
func WithUserLogin(login string) UserMonitoringOption {
	return v2.WithUserLogin(login)
}

// WithUserOrg returns the option setting the organization of the authenticated user.
func WithUserOrg(org string) UserMonitoringOption {
	return v2.WithUserOrg(org)
}

// WithUserEmail returns the option setting the email of the authenticated user.
func WithUserEmail(email string) UserMonitoringOption {
	return v2.WithUserEmail(email)
}

// WithUserName returns the option setting the name of the authenticated user.
func WithUserName(name string) UserMonitoringOption {
	return v2.WithUserName(name)
}

// WithUserSessionID returns the option setting the session ID of the authenticated user.
func WithUserSessionID(sessionID string) UserMonitoringOption {
	return v2.WithUserSessionID(sessionID)
}

// WithUserRole returns the option setting the role of the authenticated user.
func WithUserRole(role string) UserMonitoringOption {
	return v2.WithUserRole(role)
}

// WithUserScope returns the option setting the scope (authorizations) of the authenticated user.
func WithUserScope(scope string) UserMonitoringOption {
	return v2.WithUserScope(scope)
}

// WithPropagation returns the option allowing the user id to be propagated through distributed traces.
// The user id is base64 encoded and added to the datadog propagated tags header.
// This option should only be used if you are certain that the user id passed to `SetUser()` does not contain any
// personal identifiable information or any kind of sensitive data, as it will be leaked to other services.
func WithPropagation() UserMonitoringOption {
	return v2.WithPropagation()
}

// ApplyV1Options consumes a list of v1 StartSpanOptions and returns a function
// that can be used to set the corresponding v2 StartSpanConfig fields.
// This is used to adapt the v1 StartSpanOptions to the v2 StartSpanConfig.
func ApplyV1Options(opts ...ddtrace.StartSpanOption) v2.StartSpanOption {
	return internal.ApplyV1Options(opts...)
}

// ApplyV1Options consumes a list of v1 FinishOption and returns a function
// that can be used to set the corresponding v2 FinishConfig fields.
// This is used to adapt the v1 FinishConfig to the v2 FinishConfig.
func ApplyV1FinishOptions(opts ...ddtrace.FinishOption) v2.FinishOption {
	return internal.ApplyV1FinishOptions(opts...)
}

// WrapSpanV2 wraps a v2.Span into a ddtrace.Span.
// This is not intended for external use. It'll be removed when v1 is deprecated.
func WrapSpanV2(span *v2.Span) ddtrace.Span {
	return &internal.SpanV2Adapter{Span: span}
}
