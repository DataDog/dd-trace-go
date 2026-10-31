// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

// Broken type-checks (undefinedIdent is undefined), so scan must fail loudly
// instead of silently returning a partial inventory. The error is a type
// error, not a parse error, on purpose: `go mod tidy` (run on every module by
// the static-modules CI job) parses this file's imports and must keep working.
func Broken() string {
	return undefinedIdent
}
