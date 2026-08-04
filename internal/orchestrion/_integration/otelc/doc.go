// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the build-mode proof for the otelc lane. See proof_test.go.
//
// This file carries no code, but it has to exist. With only _test.go files the
// package has no Go files to build, otelc's setup skips it, and when it is the
// only package under test otelc finds no tool file, falls back to its embedded
// default rules, and instruments nothing. The proof test then fails claiming the
// build was not otelc, which is true but for the wrong reason.
package otelc
