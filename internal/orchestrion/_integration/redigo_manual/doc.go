// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package redigomanual covers an application that calls the redigo contrib
// directly inside a build that is also auto-instrumented. See manual_test.go.
//
// This file carries no code, but it has to exist. With only _test.go files the
// package has no Go files to build, so otelc skips it, falls back to its embedded
// default rules and instruments nothing. The test then skips itself for the wrong
// reason instead of asserting anything.
package redigomanual
