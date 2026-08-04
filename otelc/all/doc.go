// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package all enables every available otelc integration at once.
//
// The file carrying the imports is build-tagged out, so this one exists to keep
// the package buildable: otelc skips packages with no Go files and would then
// fall back to its embedded default rules.
package all
