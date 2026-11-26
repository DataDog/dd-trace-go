// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package vault

import (
	"context"
	"testing"

	"example.com/vaultdep/vaultconfig"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a config built in a dependency module, not
// in the module being built, is traced once.
type TestCaseDependency struct {
	base
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	tc.base.newClient(t, vaultconfig.New(tc.server.URL))
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseDependency) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
