// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build !go1.20

package gotesting

import "testing"

// getCoverage retrieves the code coverage percentage using the standard testing package.
// This function is used for Go versions prior to 1.20, where the old coverage format is the default.
// It calls testing.Coverage, which returns the current code coverage as a fraction (between 0 and 1).
// This fraction is then converted to a percentage (between 0 and 100).
//
// Returns:
//
//	A float64 representing the coverage percentage (e.g., 75 for 75% coverage).
//	An error, which is always nil in this implementation since testing.Coverage does not return an error.
func getCoverage() (float64, error) {
	return testing.Coverage() * 100, nil
}
