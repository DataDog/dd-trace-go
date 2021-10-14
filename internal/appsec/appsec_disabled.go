// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !appsec
// +build !appsec

package appsec

// Start AppSec when enabled is enabled by both using the appsec build tag and
// setting the environment variable DD_APPSEC_ENABLED to true.
func Start(*Config) {}

// Stop AppSec.
func Stop() {}
