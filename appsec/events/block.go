// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package events provides the types and interfaces for the appsec event system.
// User-facing events can be returned by the appsec package to signal that a request was blocked.
// Handling these events differently than other errors is crucial to not leak information to an attacker.
package events

var _ error = (*BlockingSecurityEvent)(nil)

// BlockingSecurityEvent is an event that signals that a request was blocked by the WAF.
// It should be handled differently than other errors to avoid leaking information to an attacker.
// If this error was returned by native types wrapped by dd-trace-go, it means that a 403 response will be written
// by appsec middleware (or any other status code defined in DataDog's UI). Therefore, the user should not write a
// response in the handler.
type BlockingSecurityEvent struct{}

func (*BlockingSecurityEvent) Error() string {
	return "request blocked by WAF"
}
