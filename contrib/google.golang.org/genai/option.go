// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

// config holds integration-level configuration for the wrapper. Individual
// options are functional and are applied by [WrapClient].
type config struct{}

// Option customizes the behavior of the tracing wrapper returned by [WrapClient].
type Option func(*config)

func defaults() *config {
	return &config{}
}
