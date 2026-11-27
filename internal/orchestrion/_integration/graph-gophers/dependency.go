// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"testing"

	"example.com/graphgophersdep"
	"github.com/stretchr/testify/require"
)

// TestCaseDependency checks that a schema parsed in a dependency module, not in
// the module being built, is traced.
type TestCaseDependency struct {
	TestCase
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	s, err := graphgophersdep.ParseSchema(schema, new(resolver))
	require.NoError(t, err)
	tc.serve(t, s)
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return spansPerRequest
}
