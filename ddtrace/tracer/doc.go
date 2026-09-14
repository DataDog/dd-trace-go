// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package tracer contains Datadog's core tracing client. It is used to trace
// requests as they flow across web servers, databases and microservices, giving
// developers visibility into bottlenecks and troublesome requests. To start the
// tracer, simply call the start method along with an optional set of options.
// By default, the trace agent is considered to be found at "localhost:8126". In a
// setup where this would be different (let's say 127.0.0.1:1234), we could do:
//
//	tracer.Start(tracer.WithAgentAddr("127.0.0.1:1234"))
//	defer tracer.Stop()
//
// The tracing client can perform trace sampling. While the trace agent
// already samples traces to reduce bandwidth usage, client sampling reduces
// performance overhead. To make use of it, the package comes with a ready-to-use
// rate sampler that can be passed to the tracer. To use it and keep only 30% of the
// requests, one would do:
//
//	s := tracer.NewRateSampler(0.3)
//	tracer.Start(tracer.WithSampler(s))
//
// More precise control of sampling rates can be configured using sampling rules.
// This can be applied based on span name, service or both, and is used to determine
// the sampling rate to apply. MaxPerSecond specifies max number of spans per second
// that can be sampled per the rule and applies only to sampling rules of type
// tracer.SamplingRuleSpan. If MaxPerSecond is not specified, the default is no limit.
//
//	rules := []tracer.SamplingRule{
//	      // sample 10% of traces with the span name "web.request"
//	      tracer.NameRule("web.request", 0.1),
//	      // sample 20% of traces for the service "test-service"
//	      tracer.ServiceRule("test-service", 0.2),
//	      // sample 30% of traces when the span name is "db.query" and the service
//	      // is "postgres.db"
//	      tracer.NameServiceRule("db.query", "postgres.db", 0.3),
//	      // sample 100% of traces when name and service match these regular expressions
//	      {Name: regexp.MustCompile("web\\..*"), Service: regexp.MustCompile("^test-"), Rate: 1.0},
//	      // sample 50% of spans when service and name match these glob patterns with no limit on the number of spans
//	      tracer.SpanNameServiceRule("web.*", "test-*", 0.5),
//	      // sample 50% of spans when service and name match these glob patterns up to 100 spans per second
//	      tracer.SpanNameServiceMPSRule("web.*", "test-*", 0.5, 100),
//	}
//	tracer.Start(tracer.WithSamplingRules(rules))
//	defer tracer.Stop()
//
// Sampling rules can also be configured at runtime using the DD_TRACE_SAMPLING_RULES and
// DD_SPAN_SAMPLING_RULES environment variables. When set, it overrides rules set by tracer.WithSamplingRules.
// The value is a JSON array of objects.
// For trace sampling rules, the "sample_rate" field is required, the "name" and "service" fields are optional.
// For span sampling rules, the "name" and "service", if specified, must be a valid glob pattern,
// i.e. a string where "*" matches any contiguous substring, even an empty string,
// and "?" character matches exactly one of any character.
// The "sample_rate" field is optional, and if not specified, defaults to "1.0", sampling 100% of the spans.
// The "max_per_second" field is optional, and if not specified, defaults to 0, keeping all the previously sampled spans.
//
//	export DD_TRACE_SAMPLING_RULES='[{"name": "web.request", "sample_rate": 1.0}]'
//	export DD_SPAN_SAMPLING_RULES='[{"service":"test.?","name": "web.*", "sample_rate": 1.0, "max_per_second":100}]'
//
// When the tracer makes a probability-based sampling decision (the agent rate,
// a global rate, or a sampling rule), it additionally expresses that decision as
// an OpenTelemetry consistent-probability-sampling pair (ot.th/ot.rv, per OTEP 235)
// on the W3C tracestate header, as the ot= list-member. This lets OpenTelemetry-native
// downstream services make and verify the same keep/drop decision and extrapolate
// span counts across a mixed Datadog/OpenTelemetry trace. An inbound ot= is honored
// and forwarded unchanged; non-probability decisions (manual keep, AppSec, or a
// rate-limiter drop) omit the threshold. This is automatic, requires no configuration,
// and leaves the existing Datadog propagation unchanged. When spans are exported over
// OTLP, the same ot= member is carried on the span's trace_state and the W3C sampled
// trace-flag is set, so the export matches what wire propagation would emit.
//
// To create spans, use the functions StartSpan and StartSpanFromContext. Both accept
// StartSpanOptions that can be used to configure the span. A span that is started
// with no parent will begin a new trace. See the function documentation for details
// on specific usage. Each trace has a hard limit of 100,000 spans, after which the
// trace will be dropped and give a diagnostic log message. In practice users should
// not approach this limit as traces of this size are not useful and impossible to
// visualize.
//
// See the contrib package ( https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib )
// for integrating datadog with various libraries, frameworks and clients.
//
// All spans created by the tracer contain a context hereby referred to as the span
// context. Note that this is different from Go's context. The span context is used
// to package essential information from a span, which is needed when creating child
// spans that inherit from it. Thus, a child span is created from a span's span context.
// The span context can originate from within the same process, but also a
// different process or even a different machine in the case of distributed tracing.
//
// To make use of distributed tracing, a span's context may be injected via a carrier
// into a transport (HTTP, RPC, etc.) to be extracted on the other end and used to
// create spans that are direct descendants of it. A couple of carrier interfaces
// which should cover most of the use-case scenarios are readily provided, such as
// HTTPCarrier and TextMapCarrier. Users are free to create their own, which will work
// with our propagation algorithm as long as they implement the TextMapReader and TextMapWriter
// interfaces. An example alternate implementation is the MDCarrier in our gRPC integration.
//
// As an example, injecting a span's context into an HTTP request would look like this.
// (See the net/http contrib package for more examples https://pkg.go.dev/github.com/DataDog/dd-trace-go/contrib/net/http/v2):
//
//	req, err := http.NewRequest("GET", "http://example.com", nil)
//	// ...
//	err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header))
//	// ...
//	http.DefaultClient.Do(req)
//
// Then, on the server side, to continue the trace one would do:
//
//	sctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Header))
//	// ...
//	span := tracer.StartSpan("child.span", tracer.ChildOf(sctx))
//
// In the same manner, any means can be used as a carrier to inject a context into a transport. Go's
// context can also be used as a means to transport spans within the same process. The methods
// StartSpanFromContext, ContextWithSpan and SpanFromContext exist for this reason.
//
// Some libraries and frameworks are supported out-of-the-box by using one
// of our integrations. You can see a list of supported integrations here:
// https://pkg.go.dev/github.com/DataDog/dd-trace-go/v2/contrib
//
// # Client-Side Stats Cardinality Controls
//
// When client-side stats computation is enabled (WithStatsComputation or
// DD_TRACE_STATS_COMPUTATION_ENABLED), the tracer aggregates span metrics
// locally before sending them to the agent. High-cardinality services can
// produce an unbounded number of unique aggregation keys, which increases
// memory usage and backend cost. Six limits cap the number of distinct values
// per aggregation dimension before collapsing excess values to a placeholder:
//
//   - DD_TRACE_STATS_CARDINALITY_LIMIT / WithStatsCardinalityLimit
//     Whole-key limit: caps the total number of unique aggregation keys per
//     flush bucket across all dimensions combined. Default: 2048.
//
//   - DD_TRACE_STATS_RESOURCE_CARDINALITY_LIMIT / WithStatsResourceCardinalityLimit
//     Per-resource limit: caps distinct resource values within a service+name
//     combination. Default: 1024.
//
//   - DD_TRACE_STATS_HTTP_ENDPOINT_CARDINALITY_LIMIT / WithStatsHTTPEndpointCardinalityLimit
//     Per-http_endpoint limit. Default: 512.
//
//   - DD_TRACE_STATS_PEER_TAGS_CARDINALITY_LIMIT / WithStatsPeerTagsCardinalityLimit
//     Per-peer-tags combination limit. Default: 512.
//
//   - DD_TRACE_STATS_ORIGIN_CARDINALITY_LIMIT / WithStatsOriginCardinalityLimit
//     Per-origin limit. Default: 20.
//
//   - DD_TRACE_STATS_ADDITIONAL_TAGS_CARDINALITY_LIMIT
//     Per-additional-metric-tags combination limit (see below). Default: 100.
//
// When a limit is reached, excess spans are still counted but their grouping
// dimension is replaced with a sentinel value. The tracer emits a
// datadog.tracer.stats.collapsed_spans statsd metric tagged with the dimension
// that was capped (e.g. collapsed:resource) so the event is observable.
//
// # Additional Metric Tags
//
// By default, client-side stats group spans by service, resource, name, type,
// HTTP method, HTTP endpoint, peer tags, and origin. Additional span tag keys
// can be included as extra grouping dimensions using:
//
//	tracer.Start(tracer.WithStatsAdditionalTags([]string{"region", "tenant_id"}))
//
// or the environment variable DD_TRACE_STATS_ADDITIONAL_TAGS (comma-separated).
// This feature requires DD_TRACE_EXPERIMENTAL_FEATURES_ENABLED=true.
//
// # Trace Protocol
//
// Client-side stats computation is independent of the Datadog trace protocol
// version (DD_TRACE_AGENT_PROTOCOL_VERSION). Disabling stats computation does
// not change the wire format used to send traces, and selecting a protocol
// version does not enable or disable stats computation. The protocol falls
// back from 1.0 to 0.4 only when the Datadog Agent does not advertise the
// /v1.0/traces endpoint.
//
// Agent capabilities are re-checked periodically, so this fallback is applied
// at runtime and not only at startup: if a running Agent stops advertising
// /v1.0/traces (for example after a rollback), the tracer switches to 0.4
// without needing a restart. A live send that the Agent rejects outright
// triggers the same switch immediately, without waiting for the next check.
//
// Either way, the downgrade is permanent for the life of the process: once
// the tracer has conclusive evidence 1.0 is unavailable, it never
// re-upgrades on its own, even if a later check reports 1.0 support again.
// This is deliberate: in a load-balanced fleet, a capability check and a
// trace send are independent requests that can land on different backends,
// so no number of healthy checks proves anything about where the next send
// goes. Re-upgrading to 1.0 requires restarting the process.
//
// A downgrade can cost already-buffered traces: a payload built while 1.0 was
// still in effect keeps going to /v1.0/traces, since it was already
// committed to that wire format, and may be rejected there; a payload the
// Agent rejects outright is dropped rather than re-encoded and redelivered on
// 0.4. Under concurrent flush traffic this is not limited to a single
// payload: several payloads can be in flight (or queued waiting for a
// connection slot) at once, so a real Agent-side rollback can cost up to the
// tracer's concurrent-connection limit worth of payloads already committed to
// 1.0 at the moment the rejection is discovered, not just the one that
// discovers it.
//
// One exception: on the 1.0 protocol, a trace-agent identifying as version
// 7.77.x, 7.78.x, or an unreleased 7.79.0 pre-release predating 7.79.0-rc.6,
// has a defect where its own stats aggregation for that protocol loses the
// span's language dimension. (The same defect exists in versions 7.73.0
// through 7.76.x, but those don't advertise /v1.0/traces by default, so the
// protocol guard above already excludes them in practice.) The tracer
// detects this from the agent's reported version and enables client-side
// stats computation regardless of DD_TRACE_STATS_COMPUTATION_ENABLED /
// WithStatsComputation(false), so that the tracer computes the affected
// stats itself instead of relying on the agent. This also enables P0 trace
// dropping, since the two capabilities are not independently controllable
// (see WithStatsComputation). To opt out, either upgrade the trace-agent to
// 7.79.0 or later, or set DD_TRACE_AGENT_PROTOCOL_VERSION=0.4. Two cases are
// explicitly excluded from this override: the Datadog Lambda extension,
// which already computes trace stats server-side (contrib/aws/datadog-lambda-go
// starts the tracer with WithStatsComputation(false) on purpose), and CI
// Visibility, whose transport never sends stats through this path regardless.
// Other trace-agent implementations that do not follow this versioning
// scheme (for example, an OpenTelemetry Collector exporter acting as a
// Datadog trace-agent) are never affected by this override.
// # Feature Flags Remote Config subscription
//
// The tracer eagerly subscribes to the FFE_FLAGS Remote Config product during Start, but only when
// the resolved Feature Flags delivery source is remote_config. By default, and whenever
// DD_FEATURE_FLAGS_CONFIGURATION_SOURCE is agentless (the default) or Feature Flags are disabled,
// this subscription is skipped: Feature Flags configuration is instead polled directly from Datadog
// over HTTPS by the openfeature package's provider, once the application creates it, with no Agent
// dependency and no Remote Config capability advertised. See the openfeature package's
// documentation for the full set of DD_FEATURE_FLAGS_* environment variables and the source
// selection rules.
package tracer // import "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
