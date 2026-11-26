// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"context"
	"testing"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseOnlyImportUse checks a file whose only use of the rueidis import is
// the rueidis.NewClient call, so the rewritten file must still use the import.
type TestCaseOnlyImportUse struct {
	addr string
}

func (tc *TestCaseOnlyImportUse) PreBootstrap(_ context.Context, t *testing.T) {
	tc.addr = startRedis(t)
}

func (*TestCaseOnlyImportUse) Setup(context.Context, *testing.T) {}

func (tc *TestCaseOnlyImportUse) Run(ctx context.Context, t *testing.T) {
	client, err := rueidis.NewClient(clientOption(tc.addr))
	require.NoError(t, err)
	defer client.Close()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	require.Error(t, client.Do(ctx, client.B().Get().Key("only-import-use").Build()).Error())
}

// ExpectedSpanCount is test.root and the GET.
func (*TestCaseOnlyImportUse) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseOnlyImportUse) ExpectedTraces() trace.Traces {
	return rootWithCommand("redis.client")
}
