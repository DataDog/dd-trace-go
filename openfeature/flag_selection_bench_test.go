// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkRequestURLWithFlagSelection measures the construction-time work for
// the largest selection that is still sent: the maximum number of distinct tags
// at the maximum length. Distinct values keep deduplication from shrinking the
// input.
func BenchmarkRequestURLWithFlagSelection(b *testing.B) {
	tags := make([]string, flagSelectionMaxValues)
	for i := range tags {
		prefix := fmt.Sprintf("%02d", i)
		tags[i] = prefix + strings.Repeat("a", flagSelectionMaxTagBytes-len(prefix))
	}
	selection := &FlagSelection{Tags: tags}
	if _, sent := requestURLWithFlagSelection(testManagedEndpoint, selection); !sent {
		b.Fatal("selection at the documented limits was not sent")
	}
	b.ReportAllocs()
	for b.Loop() {
		requestURLWithFlagSelection(testManagedEndpoint, selection)
	}
}
