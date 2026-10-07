// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"strings"
	"testing"
)

// Temporary CI probe for the nested-modules lint step: same pattern as
// appsec_commit_test.go:239 in #5420. Reverted in the next commit.
func TestLintProbe(t *testing.T) {
	n := 0
	for _, tag := range strings.Split("a,b", ",") {
		n += len(tag)
	}
	if n != 2 {
		t.Fatal(n)
	}
}
