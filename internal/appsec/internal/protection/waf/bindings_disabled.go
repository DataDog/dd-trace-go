// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Build when CGO is disabled or the target OS or Arch are not supported
//go:build !appsec || !cgo || windows || !amd64
// +build !appsec !cgo windows !amd64

package waf

import (
	"errors"
	"time"
)

type (
	// Version of the WAF.
	Version    struct{}
	wafHandle  struct{}
	wafContext struct{}
)

var errDisabledReason = errors.New(disabledReason)

// String returns the string representation of the version.
func (*Version) String() string { return "" }

// Health allows knowing if the WAF can be used. It returns the current WAF
// version and a nil error when the WAF library is healthy. Otherwise, it
// returns a nil version and an error describing the issue.
func Health() (*Version, error) {
	return nil, errDisabledReason
}

func newWAFHandle([]byte) (*wafHandle, error) { return nil, errDisabledReason }
func (*wafHandle) addresses() []string        { return nil }
func (*wafHandle) close()                     {}

func newWAFContext(*wafHandle) *wafContext { return nil }
func (*wafContext) run(map[string]interface{}, time.Duration) ([]byte, error) {
	return nil, errDisabledReason
}
func (*wafContext) close() {}
