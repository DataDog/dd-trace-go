// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package usersec

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	emitterhttpsec "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/usersec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

type Feature struct{}

func (*Feature) String() string {
	return "User Security"
}

func (*Feature) Stop() {}

func NewUserSecFeature(cfg *config.Config, rootOp dyngo.Operation) (listener.Feature, error) {
	if !cfg.SupportedAddresses.AnyOf(
		addresses.UserIDAddr,
		addresses.UserLoginAddr,
		addresses.UserOrgAddr,
		addresses.UserSessionIDAddr,
		addresses.UserLoginSuccessAddr,
		addresses.UserLoginFailureAddr) {
		return nil, nil
	}

	feature := &Feature{}
	dyngo.OnFinish(rootOp, feature.OnFinish)
	return feature, nil
}

// eventTypeName maps UserEventType to the RFC-1012 event_type tag value.
func eventTypeName(t usersec.UserEventType) string {
	switch t {
	case usersec.UserLoginSuccess:
		return "login_success"
	case usersec.UserLoginFailure:
		return "login_failure"
	case usersec.UserSet:
		return "authenticated_request"
	default:
		return "unknown"
	}
}

// frameworkFromOp walks the operation tree upward to find a HandlerOperation and returns its
// framework name. Falls back to "unknown" if none is found.
func frameworkFromOp(op dyngo.Operation) string {
	for current := op.Parent(); current != nil; current = current.Parent() {
		if h, ok := current.(*emitterhttpsec.HandlerOperation); ok {
			return h.Framework()
		}
	}
	return "unknown"
}

func (*Feature) OnFinish(op *usersec.UserLoginOperation, res usersec.UserLoginOperationRes) {
	eventType := eventTypeName(op.EventType)
	framework := frameworkFromOp(op)
	tags := []string{"event_type:" + eventType, "framework:" + framework}

	if res.UserLogin == "" &&
		(op.EventType == usersec.UserLoginSuccess || op.EventType == usersec.UserLoginFailure) {
		telemetry.Count(telemetry.NamespaceAppSec, "instrum.user_auth.missing_user_login", tags).Submit(1)
	}
	if res.UserID == "" &&
		(op.EventType == usersec.UserLoginSuccess || op.EventType == usersec.UserSet) {
		telemetry.Count(telemetry.NamespaceAppSec, "instrum.user_auth.missing_user_id", tags).Submit(1)
	}

	builder := addresses.NewAddressesBuilder().
		WithUserID(res.UserID).
		WithUserLogin(res.UserLogin).
		WithUserOrg(res.UserOrg).
		WithUserSessionID(res.SessionID)

	switch op.EventType {
	case usersec.UserLoginSuccess:
		builder = builder.WithUserLoginSuccess().
			WithUserID(res.UserID).
			WithUserLogin(res.UserLogin).
			WithUserOrg(res.UserOrg).
			WithUserSessionID(res.SessionID)
	case usersec.UserLoginFailure:
		builder = builder.WithUserLoginFailure().
			WithUserID(res.UserID).
			WithUserLogin(res.UserLogin).
			WithUserOrg(res.UserOrg)
	case usersec.UserSet:
		builder = builder.WithUserID(res.UserID).
			WithUserLogin(res.UserLogin).
			WithUserOrg(res.UserOrg).
			WithUserSessionID(res.SessionID)
	}

	dyngo.EmitData(op, waf.RunEvent{
		Operation:      op,
		RunAddressData: builder.Build(),
	})
}
