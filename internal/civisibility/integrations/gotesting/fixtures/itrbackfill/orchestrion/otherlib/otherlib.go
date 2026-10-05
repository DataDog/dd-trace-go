// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otherlib

// Double returns the input doubled for the multi-package ITR backfill fixture.
func Double(value int) int {
	result := value
	result += value
	return result
}
