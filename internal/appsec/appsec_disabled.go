// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !appsec
// +build !appsec

package appsec

// Start AppSec when the environment variable DD_APPSEC_ENABLED is set to true.
func Start(*Config) (enabled bool) {
	return false
}

// Stop AppSec.
func Stop() {}
