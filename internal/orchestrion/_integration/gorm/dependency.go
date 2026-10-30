// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gorm

import (
	"context"
	"testing"

	"example.com/gormdep"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a connection opened in a dependency module,
// not in the module being built, is traced.
type TestCaseDependency struct {
	db *gorm.DB
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.db, err = gormdep.Open()
	require.NoError(t, err)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	runSelectOne(ctx, t, tc.db)
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return selectOneTraces()
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return 6
}
