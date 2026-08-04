// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package lib

// Answer returns a stable value used by the ITR backfill fixture.
func Answer() int {
	value := 40
	value += 2
	return value
}
