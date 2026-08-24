// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package status exposes the status of appsec, i.e. whether or
// not it is currently enabled. This exists so that other dd-trace-go packages
// can query this status without depending on all of appsec.
package status

import "sync/atomic"

var enabled atomic.Bool

// Enabled reports whether appsec is enabled.
func Enabled() bool {
	return enabled.Load()
}

// SetEnabled sets whether appsec is enabled.
func SetEnabled(value bool) {
	enabled.Store(value)
}
