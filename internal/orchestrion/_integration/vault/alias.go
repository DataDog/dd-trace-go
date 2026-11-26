// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package vault

import (
	"context"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a config is traced once when the vault
// api package is imported under another name.
type TestCaseAliasedImport struct {
	base
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	tc.base.newClient(t, &vaultapi.Config{Address: tc.server.URL})
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
