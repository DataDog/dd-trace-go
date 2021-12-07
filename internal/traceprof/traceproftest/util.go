// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package traceproftest

import "strconv"

// ValidSpanID returns true if id is a valid span id (random.Uint64()).
func ValidSpanID(id string) bool {
	val, err := strconv.ParseUint(id, 10, 64)
	return err == nil && val > 0
}
