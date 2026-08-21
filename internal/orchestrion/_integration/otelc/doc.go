// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the build-mode proof for the otelc lane. See proof_test.go.
//
// The package needs this non-test file. With only _test.go files it has nothing
// to build, so otelc applies no rules and the proof test fails for the wrong
// reason.
package otelc
