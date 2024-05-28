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
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var appsecDisabledLog sync.Once

// MonitorParsedHTTPBody runs the security monitoring rules on the given *parsed*
// HTTP request body and returns if the HTTP request is suspicious and configured to be blocked.
// The given context must be the HTTP request context as returned
// by the Context() method of an HTTP request. Calls to this function are ignored if
// AppSec is disabled or the given context is incorrect.
// Note that passing the raw bytes of the HTTP request body is not expected and would
// result in inaccurate attack detection.
// This function always returns nil when appsec is disabled.
func MonitorParsedHTTPBody(ctx context.Context, body interface{}) error {
	if !appsec.Enabled() {
		appsecDisabledLog.Do(func() { log.Warn("appsec: not enabled. Body blocking checks won't be performed.") })
		return nil
	}
	return httpsec.MonitorParsedBody(ctx, body)
}

// SetUser wraps tracer.SetUser() and extends it with user blocking.
// On top of associating the authenticated user information to the service entry span,
// it checks whether the given user ID is blocked or not by returning an error when it is.
// A user ID is blocked when it is present in your denylist of users to block at https://app.datadoghq.com/security/appsec/denylist
// When an error is returned, the caller must immediately abort its execution and the
// request handler's. The blocking response will be automatically sent by the
// APM tracer middleware on use according to your blocking configuration.
// This function always returns nil when appsec is disabled and doesn't block users.
func SetUser(ctx context.Context, id string, opts ...tracer.UserMonitoringOption) error {
	s, ok := tracer.SpanFromContext(ctx)
	if !ok {
		log.Debug("appsec: could not retrieve span from context. User ID tag won't be set")
		return nil
	}
	tracer.SetUser(s, id, opts...)
	if !appsec.Enabled() {
		appsecDisabledLog.Do(func() { log.Warn("appsec: not enabled. User blocking checks won't be performed.") })
		return nil
	}
	return sharedsec.MonitorUser(ctx, id)
}

// TrackUserLoginSuccessEvent sets a successful user login event, with the given
// user id and optional metadata, as service entry span tags. It also calls
// SetUser() to set the currently authenticated user, along with the given
// tracer.UserMonitoringOption options. As documented in SetUser(), an
// error is returned when the given user ID is blocked by your denylist. Cf.
// SetUser()'s documentation for more details.
// The service entry span is obtained through the given Go context which should
// contain the currently running span. This function does nothing when no span
// is found in the given Go context and logs an error message instead.
// Such events trigger the backend-side events monitoring, such as the Account
// Take-Over (ATO) monitoring, ultimately blocking the IP address and/or user id
// associated to them.
func TrackUserLoginSuccessEvent(ctx context.Context, uid string, md map[string]string, opts ...tracer.UserMonitoringOption) error {
	span := getRootSpan(ctx)
	if span == nil {
		return nil
	}

	const tagPrefix = "appsec.events.users.login.success."
	span.SetTag(tagPrefix+"track", true)
	for k, v := range md {
		span.SetTag(tagPrefix+k, v)
	}
	span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
	return SetUser(ctx, uid, opts...)
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
