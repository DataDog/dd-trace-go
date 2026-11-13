// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package zerolog

import (
	"context"
	"testing"

	zl "github.com/rs/zerolog"
)

// TestCaseAliasedImport covers a logger built with zerolog.New when zerolog is
// imported under another name.
type TestCaseAliasedImport struct {
	correlation
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	tc.logger = zl.New(tc.newLogs())
}
