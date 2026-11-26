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

// TestCaseValueLiteral checks that an api.Config{} value literal is traced
// once.
type TestCaseValueLiteral struct {
	base
}

func (tc *TestCaseValueLiteral) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	cfg := api.Config{Address: tc.server.URL}
	tc.base.newClient(t, &cfg)
}

func (tc *TestCaseValueLiteral) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseValueLiteral) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseValueLiteral) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
