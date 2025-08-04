// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"time"
)

// Config holds the configuration for the App and API Protection Datadog product.
type Config struct {
	// WAFTimeout is the maximum WAF execution time (default to 1ms).
	WAFTimeout time.Duration
	// TraceRateLimit is the AppSec trace rate limit (default to 100).
	TraceRateLimit int64
	// APISecDisabled set to true will disable the AppSec API Security features.
	APISecDisabled bool
	// RASPDisabled determines whether RASP features are enabled or not.
	RASPDisabled bool
	// BlockingUnavailable is true when the application run in an environment where blocking is not possible
	BlockingUnavailable bool
	// StandaloneMode can be set to true if you don't plan to use the tracer and not be billed for it.
	StandaloneMode bool

	// ProxyMode determines how the AppSec product will behave when used as a proxy extension.
	ProxyMode ProxyMode
}

// ProxyMode is reserved for contrib that uses the App and API Protection product as proxy extension.
type ProxyMode int

const (
	// ProxyModeDisabled means that the AppSec product is not used as a proxy extension.
	ProxyModeDisabled ProxyMode = iota
	// ProxyModeSync means that the AppSec product is used as a synchronous proxy extension that blocks can the request
	ProxyModeSync
	// ProxyModeAsync means that the AppSec product is used as an asynchronous proxy extension that does not block the request
	ProxyModeAsync
)
