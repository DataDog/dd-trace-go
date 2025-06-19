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
	"maps"
	"strconv"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/usersec"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var appsecDisabledLog sync.Once

type CollectionMode string

const (
	CollectionModeSDK CollectionMode = "sdk"
)

// MonitorParsedHTTPBody runs the security monitoring rules on the given *parsed*
// HTTP request body and returns if the HTTP request is suspicious and configured to be blocked.
// The given context must be the HTTP request context as returned
// by the Context() method of an HTTP request. Calls to this function are ignored if
// AppSec is disabled or the given context is incorrect.
// Note that passing the raw bytes of the HTTP request body is not expected and would
// result in inaccurate attack detection.
// This function always returns nil when appsec is disabled.
func MonitorParsedHTTPBody(ctx context.Context, body any) error {
	if !appsec.Enabled() {
		appsecDisabledLog.Do(func() { log.Warn("appsec: not enabled. Body blocking checks won't be performed.") })
		return nil
	}
	return httpsec.MonitorParsedBody(ctx, body)
}

// MonitorHTTPResponseBody runs the security monitoring rules on the given
// response body (in object form, not encoded as the literal HTTP response body
// payload bytes), and returns an error if the HTTP response is configured to be
// blocked. The given context must be the HTTP request context as returned by
// the [net/http.Request.Context] method, or equivalent. Calls to this function
// are ignored if AppSec is disabled or the provided context is incorrect.
func MonitorHTTPResponseBody(ctx context.Context, body any) error {
	if !appsec.Enabled() {
		appsecDisabledLog.Do(func() { log.Warn("appsec: not enabled. Body blocking checks won't be performed.") })
		return nil
	}
	return httpsec.MonitorResponseBody(ctx, body)
}

// SetUser wraps [tracer.SetUser] and extends it with user blocking.
// On top of associating the authenticated user information to the service entry span,
// it checks whether the given user ID is blocked or not by returning an error when it is.
// A user ID is blocked when it is present in your denylist of users to block at https://app.datadoghq.com/security/appsec/denylist
// When an error is returned, the caller must immediately abort its execution and the
// request handler's. The blocking response will be automatically sent by the
// APM tracer middleware on use according to your blocking configuration.
// This function always returns nil when appsec is disabled and doesn't block users.
func SetUser(ctx context.Context, id string, opts ...tracer.UserMonitoringOption) error {
	return setUser(ctx, id, usersec.UserSet, CollectionModeSDK, opts)
}

func setUser(ctx context.Context, id string, userEventType usersec.UserEventType, collectionMode CollectionMode, opts []tracer.UserMonitoringOption) error {
	s, ok := tracer.SpanFromContext(ctx)
	if !ok {
		log.Debug("appsec: user event monitoring SDK: could not retrieve span from context. User ID tag won't be set")
		return nil
	}

	tracer.SetUser(s, id, opts...)

	// Record that the user collection mode is SDK
	s.Root().SetTag("_dd.appsec.user.collection_mode", collectionMode)

	if !appsec.Enabled() {
		appsecDisabledLog.Do(func() { log.Warn("appsec: not enabled. User blocking checks won't be performed.") })
		// Not returning here, as we still want to record the relevant span tags (just no WAF call).
	}

	op, errPtr := usersec.StartUserLoginOperation(ctx, userEventType, usersec.UserLoginOperationArgs{})
	login, userOrg, sessionID := getMetadata(opts)
	op.Finish(usersec.UserLoginOperationRes{
		UserID:    id,
		UserLogin: login,
		UserOrg:   userOrg,
		SessionID: sessionID,
	})

	return *errPtr
}

// TrackUserLoginSuccess denotes a successful user login event, which is used
// by back-end side event monitoring, such as Account Take-Over (ATO)
// monitoring, ultimately allowing IP address and/or user ID deny-lists to be
// configured in order to block associated malicious activity.
//
// The login is the username that was provided by the user as part of
// the authentication attempt, and a single user may have multiple different
// logins (i.e; user name, email address, etc...). The user however has exactly
// one user ID which canonically identifies them.
//
// The provided metadata is attached to the successful user login event.
//
// This function calso calls [SetUser] with the provided user ID and login, as
// well as any provided [tracer.UserMonitoringOption]s, and returns an error if
// the provided user ID is found to be on a configured deny list. See the
// documentation for [SetUser] for more information.
func TrackUserLoginSuccess(ctx context.Context, login string, uid string, md map[string]string, opts ...tracer.UserMonitoringOption) error {
	telemetry.Count(telemetry.NamespaceAppSec, "sdk.event", []string{"event_type:login_success", "sdk_version:v2"}).Submit(1)

	// We need to make sure the metadata contains the correct `usr.id` and
	// `usr.login` values, so we clone the metadata map and set these two.
	md = maps.Clone(md)
	if md == nil {
		md = make(map[string]string, 2)
	}
	md["usr.login"] = login
	if uid != "" {
		md["usr.id"] = uid
	}

	TrackCustomEvent(ctx, "users.login.success", md)
	return setUser(ctx, uid, usersec.UserLoginSuccess, CollectionModeSDK, append(opts, tracer.WithUserLogin(login)))
}

// TrackUserLoginFailure denotes a failed user login event, which is used by
// back-end side event monitoring, such as Account Take-Over (ATO) monitoring,
// ultimately allowing IP address and/or user ID deny-lists to be configured in
// order to block associated malicious activity.
//
// The login is the username that was provided by the user as part of
// the authentication attempt, and a single user may have multiple different
// logins (i.e; user name, email address, etc...).
//
// The exists argument allows to distinguish whether the user for which a login
// attempt failed exists in the system or not, which is usedul when sifting
// through login activity in search for malicious behavior & compromise.
//
// The provided metata is attached to the failed user login event.
func TrackUserLoginFailure(ctx context.Context, login string, exists bool, md map[string]string) {
	telemetry.Count(telemetry.NamespaceAppSec, "sdk.event", []string{"event_type:login_failure", "sdk_version:v2"}).Submit(1)

	// We need to make sure the metadata contains the correct information
	md = maps.Clone(md)
	if md == nil {
		md = make(map[string]string, 2)
	}
	md["usr.exists"] = strconv.FormatBool(exists)
	md["usr.login"] = login

	TrackCustomEvent(ctx, "users.login.failure", md)

	op, _ := usersec.StartUserLoginOperation(ctx, usersec.UserLoginFailure, usersec.UserLoginOperationArgs{})
	op.Finish(usersec.UserLoginOperationRes{UserLogin: login})
}

// TrackCustomEvent sets a custom event as service entry span tags. This span is
// obtained through the given Go context which should contain the currently
// running span. This function does nothing when no span is found in the given
// Go context, along with an error message.
// Such events trigger the backend-side events monitoring ultimately blocking
// the IP address and/or user id associated to them.
func TrackCustomEvent(ctx context.Context, name string, md map[string]string) {
	telemetry.Count(telemetry.NamespaceAppSec, "sdk.event", []string{"event_type:custom", "sdk_version:v1"}).Submit(1)

	span := getRootSpan(ctx)
	if span == nil {
		return
	}

	tagPrefix := "appsec.events." + name + "."
	span.SetTag("_dd."+tagPrefix+"sdk", "true")
	span.SetTag(tagPrefix+"track", "true")
	span.SetTag(ext.ManualKeep, true)
	for k, v := range md {
		span.SetTag(tagPrefix+k, v)
	}
}

// Return the root span from the span stored in the given Go context.
func getRootSpan(ctx context.Context) *tracer.Span {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		log.Warn("appsec: user event monitoring SDK: could not find a span in the provided context.Context")
		return nil
	}
	return span.Root()
}

func getMetadata(opts []tracer.UserMonitoringOption) (login string, org string, sessionID string) {
	cfg := tracer.UserMonitoringConfig{
		Metadata: make(map[string]string),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.Login, cfg.Org, cfg.SessionID
}
