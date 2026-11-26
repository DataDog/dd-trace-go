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

// TestCasePointerLiteral checks that a &api.Config{} literal without an
// HttpClient is traced once.
type TestCasePointerLiteral struct {
	base
}

func (tc *TestCasePointerLiteral) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	tc.base.newClient(t, &api.Config{Address: tc.server.URL})
}

func (tc *TestCasePointerLiteral) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCasePointerLiteral) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCasePointerLiteral) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
