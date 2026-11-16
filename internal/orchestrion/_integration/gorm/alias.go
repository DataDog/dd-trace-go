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
	gormv1 "gorm.io/gorm"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a connection is traced when gorm is
// imported under another name.
type TestCaseAliasedImport struct {
	db *gormv1.DB
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.db, err = gormv1.Open(sqlite.Open("file:alias?mode=memory"))
	require.NoError(t, err)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	runSelectOne(ctx, t, tc.db)
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return selectOneTraces()
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 6
}
