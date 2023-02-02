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
	"errors"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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

type userMonitoringError struct {
	shouldBlock bool
	err         error
}

func (err *userMonitoringError) ShouldBlock() bool {
	return err.shouldBlock
}
func (err *userMonitoringError) Error() string {
	return err.err.Error()
}

// SetUser associates user information to the current trace which the
// provided context belongs to. The options can be used to tune which user
// bit of information gets monitored. In case of distributed traces,
// the user id can be propagated across traces using the WithPropagation() option.
// See https://docs.datadoghq.com/security_platform/application_security/setup_and_configure/?tab=set_user#add-user-information-to-traces
// A nil returned error means everything went well. A not nil error can be used to know whether the user should be blocked or not.
func SetUser(ctx context.Context, id string, opts ...tracer.UserMonitoringOption) *userMonitoringError {
	if !appsec.Enabled() {
		return &userMonitoringError{
			err: errors.New("AppSec is not enabled"),
		}
	}
	s, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return &userMonitoringError{
			err: errors.New("Could not retrieve span from context"),
		}
	}
	tracer.SetUser(s, id, opts...)
	if httpsec.MonitorUser(ctx, id) {
		return &userMonitoringError{
			shouldBlock: true,
			err:         errors.New("Suspicious user detected. Associated requests should be blocked."),
		}
	}
	return nil
}

// TrackUserLoginSuccessEvent sets a successful user login event, with the given
// user id and optional metadata, as service entry span tags. It also calls
// tracer.SetUser() to set the currently identified user, along with the given
// tracer.UserMonitoringOption options.
// The service entry span is obtained through the given Go context which should
// contain the currently running span. This function does nothing when no span
// is found in the given Go context and logs an error message instead.
// Such events trigger the backend-side events monitoring, such as the Account
// Take-Over (ATO) monitoring, ultimately blocking the IP address and/or user id
// associated to them.
func TrackUserLoginSuccessEvent(ctx context.Context, uid string, md map[string]string, opts ...tracer.UserMonitoringOption) {
	span := getRootSpan(ctx)
	if span == nil {
		return
	}

	const tagPrefix = "appsec.events.users.login.success."
	span.SetTag(tagPrefix+"track", true)
	for k, v := range md {
		span.SetTag(tagPrefix+k, v)
	}
	span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
	tracer.SetUser(span, uid, opts...)
}

// TrackUserLoginFailureEvent sets a failed user login event, with the given
// user id and the optional metadata, as service entry span tags. The exists
// argument allows to distinguish whether the given user id actually exists or
// not.
// The service entry span is obtained through the given Go context which should
// contain the currently running span. This function does nothing when no span
// is found in the given Go context and logs an error message instead.
// Such events trigger the backend-side events monitoring, such as the Account
// Take-Over (ATO) monitoring, ultimately blocking the IP address and/or user id
// associated to them.
func TrackUserLoginFailureEvent(ctx context.Context, uid string, exists bool, md map[string]string) {
	span := getRootSpan(ctx)
	if span == nil {
		return
	}

	const tagPrefix = "appsec.events.users.login.failure."
	span.SetTag(tagPrefix+"track", true)
	span.SetTag(tagPrefix+"usr.id", uid)
	span.SetTag(tagPrefix+"usr.exists", exists)
	for k, v := range md {
		span.SetTag(tagPrefix+k, v)
	}
	span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
}

// TrackCustomEvent sets a custom event as service entry span tags. This span is
// obtained through the given Go context which should contain the currently
// running span. This function does nothing when no span is found in the given
// Go context, along with an error message.
// Such events trigger the backend-side events monitoring ultimately blocking
// the IP address and/or user id associated to them.
func TrackCustomEvent(ctx context.Context, name string, md map[string]string) {
	span := getRootSpan(ctx)
	if span == nil {
		return
	}

	tagPrefix := "appsec.events." + name + "."
	span.SetTag(tagPrefix+"track", true)
	span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
	for k, v := range md {
		span.SetTag(tagPrefix+k, v)
	}
}

// Return the root span from the span stored in the given Go context if it
// implements the Root method. It returns nil otherwise.
func getRootSpan(ctx context.Context) tracer.Span {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		log.Error("appsec: could not find a span in the given Go context")
		return nil
	}
	type rooter interface {
		Root() tracer.Span
	}
	if lrs, ok := span.(rooter); ok {
		return lrs.Root()
	}
	log.Error("appsec: could not access the root span")
	return nil
}
