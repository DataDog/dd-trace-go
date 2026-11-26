// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package vault

import (
	"context"
	"testing"

	"github.com/hashicorp/vault/api"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// packageLevelConfig is a literal outside any function, built before any init
// function of this package runs.
var packageLevelConfig = &api.Config{}

// TestCasePackageLevelConfig checks that a config written as a package-level
// literal is traced once.
type TestCasePackageLevelConfig struct {
	base
}

func (tc *TestCasePackageLevelConfig) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	packageLevelConfig.Address = tc.server.URL
	tc.base.newClient(t, packageLevelConfig)
}

func (tc *TestCasePackageLevelConfig) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCasePackageLevelConfig) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCasePackageLevelConfig) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
