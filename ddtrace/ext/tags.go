// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package ext contains a set of Datadog-specific constants. Most of them are used
// for setting span metadata.
package ext

const (
	// TargetHost sets the target host address.
	TargetHost = "out.host"

	// TargetPort sets the target host port.
	TargetPort = "out.port"

	// SamplingPriority is the tag that marks the sampling priority of a span.
	// Deprecated in favor of ManualKeep and ManualDrop.
	SamplingPriority = "sampling.priority"

	// SQLType sets the sql type tag.
	SQLType = "sql"

	// SQLQuery sets the sql query tag on a span.
	SQLQuery = "sql.query"

	// HTTPMethod specifies the HTTP method used in a span.
	HTTPMethod = "http.method"

	// HTTPCode sets the HTTP status code as a tag.
	HTTPCode = "http.status_code"

	// HTTPURL sets the HTTP URL for a span.
	HTTPURL = "http.url"

	// HTTPUserAgent is the user agent header value of the HTTP request.
	HTTPUserAgent = "http.useragent"

	// SpanName is a pseudo-key for setting a span's operation name by means of
	// a tag. It is mostly here to facilitate vendor-agnostic frameworks like Opentracing
	// and OpenCensus.
	SpanName = "span.name"

	// SpanType defines the Span type (web, db, cache).
	SpanType = "span.type"

	// ServiceName defines the Service name for this Span.
	ServiceName = "service.name"

	// Version is a tag that specifies the current application version.
	Version = "version"

	// ResourceName defines the Resource name for the Span.
	ResourceName = "resource.name"

	// Error specifies the error tag. It's value is usually of type "error".
	Error = "error"

	// ErrorMsg specifies the error message.
	ErrorMsg = "error.msg"

	// ErrorType specifies the error type.
	ErrorType = "error.type"

	// ErrorStack specifies the stack dump.
	ErrorStack = "error.stack"

	// ErrorDetails holds details about an error which implements a formatter.
	ErrorDetails = "error.details"

	// Environment specifies the environment to use with a trace.
	Environment = "env"

	// EventSampleRate specifies the rate at which this span will be sampled
	// as an APM event.
	EventSampleRate = "_dd1.sr.eausr"

	// AnalyticsEvent specifies whether the span should be recorded as a Trace
	// Search & Analytics event.
	AnalyticsEvent = "analytics.event"

	// SpanSamplingMechanism is the metric key holding a span sampling rule that a span was kept on
	SpanSamplingMechanism = "_dd.span_sampling.mechanism"

	// SingleSpanSamplingMechanism is a constant reserved to indicate that a span was kept on account of a span sampling rule.
	SingleSpanSamplingMechanism = 8

	// SingleSpanSamplingRuleRate is the metric key containing the configured sampling probability for the span sampling rule.
	SingleSpanSamplingRuleRate = "_dd.span_sampling.rule_rate"

	// SingleSpanSamplingMPS is the metric key contains the configured limit for the span sampling rule that the span matched.
	//	// If there is no configured limit, then this tag is omitted.
	SingleSpanSamplingMPS = "_dd.span_sampling.max_per_second"

	// ManualKeep is a tag which specifies that the trace to which this span
	// belongs to should be kept when set to true.
	ManualKeep = "manual.keep"

	// ManualDrop is a tag which specifies that the trace to which this span
	// belongs to should be dropped when set to true.
	ManualDrop = "manual.drop"

	// RuntimeID is a tag that contains a unique id for this process.
	RuntimeID = "runtime-id"
)
