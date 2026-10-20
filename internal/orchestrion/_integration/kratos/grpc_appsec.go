// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"testing"
)

type AppSecGRPCCase struct{ GRPCTestCase }

func (*AppSecGRPCCase) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../appsec/testdata/blocking.json")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1h")
}

func (tc *AppSecGRPCCase) Run(ctx context.Context, t *testing.T) {
	tc.attack = true
	tc.GRPCTestCase.Run(ctx, t)
}
