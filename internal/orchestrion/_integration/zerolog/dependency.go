// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package zerolog

import (
	"context"
	"testing"

	"example.com/zerologdep"
)

// TestCaseDependencyNew covers a zerolog.New call in a dependency module, not
// in the module being built.
type TestCaseDependencyNew struct {
	correlation
}

func (tc *TestCaseDependencyNew) Setup(context.Context, *testing.T) {
	tc.logger = zerologdep.NewLogger(tc.newLogs())
}
