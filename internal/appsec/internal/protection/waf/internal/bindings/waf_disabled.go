// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Build when CGO is disabled or the target OS or Arch are not supported
//go:build !appsec || !cgo || windows || !amd64
// +build !appsec !cgo windows !amd64

package bindings

import (
	"errors"
	"time"
)

type (
	Version    struct{}
	WAF        struct{}
	WAFContext struct{}
)

var errDisabledReason = errors.New(disabledReason)

func (*Version) String() string { return "" }

func Health() (*Version, error) {
	return nil, errDisabledReason
}

func NewWAF([]byte) (*WAF, error) {
	return nil, errDisabledReason
}

func (*WAF) Addresses() []string {
	return nil
}

func (*WAF) Close() error {
	return errDisabledReason
}

func NewWAFContext(*WAF) *WAFContext {
	return nil
}

func (*WAFContext) Run(map[string]interface{}, time.Duration) (Action, []byte, error) {
	return 0, nil, errDisabledReason
}

func (*WAFContext) Close() error {
	return errDisabledReason
}
