// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package appsec

import (
	"context"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/usersec"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

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
//
// Deprecated: use [TrackUserLoginSuccess] instead. It requires collection of
// the user login, which is useful for detecting account takeover attacks.
func TrackUserLoginSuccessEvent(ctx context.Context, uid string, md map[string]string, opts ...tracer.UserMonitoringOption) error {
	telemetry.Count(telemetry.NamespaceAppSec, "sdk.event", []string{"event_type:login_success", "sdk_version:v1"}).Submit(1)

	login, _, _ := getMetadata(opts)
	return TrackUserLoginSuccess(ctx, login, uid, md, opts...)
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
//
// Deprecated: use [TrackUserLoginFailure] instead. It collects the user login,
// which is what is available during a failed login attempt, instead of the user
// ID, which is oftern not (especially when the user does not exist).
func TrackUserLoginFailureEvent(ctx context.Context, uid string, exists bool, md map[string]string) {
	telemetry.Count(telemetry.NamespaceAppSec, "sdk.event", []string{"event_type:login_failure", "sdk_version:v1"}).Submit(1)

	span := getRootSpan(ctx)
	if span == nil {
		return
	}

	// We need to do the first call to SetTag ourselves because the map taken by TrackCustomEvent is map[string]string
	// and not map [string]any, so the `exists` boolean variable does not fit int
	span.SetTag("appsec.events.users.login.failure.usr.exists", strconv.FormatBool(exists))
	span.SetTag("appsec.events.users.login.failure.usr.id", uid)

	TrackCustomEvent(ctx, "users.login.failure", md)

	op, _ := usersec.StartUserLoginOperation(ctx, usersec.UserLoginFailure, usersec.UserLoginOperationArgs{})
	op.Finish(usersec.UserLoginOperationRes{UserID: uid})
}
