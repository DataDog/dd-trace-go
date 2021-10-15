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
	Version struct{}
	// Handle represents an instance of the WAF for a given ruleset.
	Handle struct{}
	// Context is a WAF execution context.
	Context struct{}
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

func NewHandle([]byte) (*Handle, error) { return nil, errDisabledReason }
func (*Handle) Addresses() []string     { return nil }
func (*Handle) Close()                  {}

func NewContext(*Handle) *Context { return nil }
func (*Context) Run(map[string]interface{}, time.Duration) ([]byte, error) {
	return nil, errDisabledReason
}
func (*Context) Close() {}
