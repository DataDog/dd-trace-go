// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the build-mode proof for the otelc lane. See proof_test.go.
//
// This file carries no code, but it has to exist: with only _test.go files the
// package has nothing to build, so otelc skips it and applies no rules at all,
// making the proof test fail for the wrong reason.
package otelc
