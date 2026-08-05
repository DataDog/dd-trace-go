// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the build-mode proof for the otelc lane. See proof_test.go.
//
// This file carries no code, but it has to exist. With only _test.go files the
// package has no Go files to build, so otelc's setup skips it, and when it is the
// package under test nothing is matched at all (.otelc-build/matched.json is
// null), not even rules targeting other packages. The proof test then fails
// claiming the build was not otelc, which is true but for the wrong reason.
package otelc
