// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package appsec provides application security features in the form of SDK
// functions that can be manually called to monitor specific code paths and data.
// Application Security is currently transparently integrated into the APM tracer
// and cannot be used nor started alone at the moment.
// You can read more on how to enable and start Application Security for Go at
// https://docs.datadoghq.com/security_platform/application_security/getting_started/go
package appsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
)

// MonitorParsedHTTPBody runs the security monitoring rules on the given *parsed*
// HTTP request body. The given context must be the HTTP request context as returned
// by the Context() method of an HTTP request. Calls to this function are ignored if
// AppSec is disabled or the given context is incorrect.
// Note that passing the raw bytes of the HTTP request body is not expected and would
// result in inaccurate attack detection.
func MonitorParsedHTTPBody(ctx context.Context, body interface{}) {
	if appsec.Enabled() {
		httpsec.MonitorParsedBody(ctx, body)
	}
	// bonus: use sync.Once to log a debug message once if AppSec is disabled
}

// TrackUserLoginEvent logs a user login event with the given user id, uniquely
// identifying them, and whether the login attempt was successful or not.
// This event is set as service entry span tags of the given span.
// The options can be used to add extra information to the event.
// Note that in case of a successful login event, this function calls
// tracer.SetUser() in order to also set the current user to the service entry
// span too.
func TrackUserLoginEvent(span tracer.Span, uid string, successful bool, opts ...tracer.UserMonitoringOption) {
	span = getLocalRootSpan(span)
	if span == nil {
		return
	}

	var tagPrefix string
	if successful {
		tagPrefix = "appsec.events.users.login.success."
		tracer.SetUser(span, uid, opts...)
	} else {
		tagPrefix = "appsec.events.users.login.failure."
		span.SetTag(tagPrefix+"usr.id", uid)
	}

	span.SetTag(tagPrefix+"track", "true")
	span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
}

// TrackCustomEvent logs a custom event with the given name and metadata.
// This event is set as service entry span tags of the given span.
// The options can be used to add extra information to the event.
// Note that in case of a successful login event, this function calls
// tracer.SetUser() in order to also set the current user to the service entry
// span too.
func TrackCustomEvent(span tracer.Span, name string, md map[string]string) {
	if span = getLocalRootSpan(span); span == nil {
		return
	}

	tagPrefix := "appsec.events." + name + "."
	span.SetTag(tagPrefix+"track", "true")
	span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
	for k, v := range md {
		span.SetTag(tagPrefix+k, v)
	}
}

// Return the local root span if the given span implements the LocalRootSpan
// method. It returns the given span otherwise.
func getLocalRootSpan(s tracer.Span) tracer.Span {
	type localRootSpanner interface {
		LocalRootSpan() tracer.Span
	}
	if lrs, ok := s.(localRootSpanner); ok {
		s = lrs.LocalRootSpan()
	}
	return s
}
