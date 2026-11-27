// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgDB is opened during package initialization, before the tracer starts.
var pkgDB, pkgErr = gorm.Open(sqlite.Open("file:var?mode=memory"))

// TestCasePackageVar checks that a connection opened in a package-level
// variable is traced once.
type TestCasePackageVar struct{}

func (*TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	require.NoError(t, pkgErr)
}

func (*TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	runSelectOne(ctx, t, pkgDB)
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return selectOneTraces()
}

// ExpectedSpanCount is the root, gorm and sqlite spans. The connection was
// opened before the tracer started, so its spans are not recorded.
func (*TestCasePackageVar) ExpectedSpanCount() int {
	return 3
}
