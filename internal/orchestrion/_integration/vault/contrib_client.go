// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package vault

import (
	"context"
	"testing"

	"github.com/hashicorp/vault/api"

	vaulttrace "github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseContribHTTPClient checks that a literal whose HttpClient already
// comes from the contrib gets one vault span, not two.
type TestCaseContribHTTPClient struct {
	base
}

func (tc *TestCaseContribHTTPClient) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	tc.base.newClient(t, &api.Config{
		Address:    tc.server.URL,
		HttpClient: vaulttrace.NewHTTPClient(),
	})
}

func (tc *TestCaseContribHTTPClient) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseContribHTTPClient) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseContribHTTPClient) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
