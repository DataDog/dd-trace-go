// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package brokendep

// Broken type-checks (undefinedIdent is undefined). This module stands in
// for an external dependency of testdata/brokenimport: the type error lives
// here, not in the importing package, so it does not appear in Errors on the
// packages matched by "./..." from brokenimport's root.
func Broken() string {
	return undefinedIdent
}
