// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import (
	"context"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// Built while the package initializes, so it checks that the contrib is
// initialized first.
var pkgSchema, pkgSchemaErr = graphql.NewSchema(helloConfig())

// TestCasePackageVar checks that a schema built in a package-level variable is
// traced.
type TestCasePackageVar struct{}

func (*TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	require.NoError(t, pkgSchemaErr)
}

func (*TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	queryHello(ctx, t, pkgSchema)
}

func (*TestCasePackageVar) ExpectedSpanCount() int {
	return spansPerQuery
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return expectHelloTrace()
}
