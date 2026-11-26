// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"testing"

	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/esfake"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgFake and pkgClient are created during package initialization, before the
// tracer starts. pkgFake runs until the test binary exits.
var (
	pkgFake, pkgFakeErr = esfake.Start(nil)
	pkgClient, pkgErr   = elasticsearch8.NewClient(elasticsearch8.Config{Addresses: fakeAddrs(pkgFake)})
)

func fakeAddrs(s *esfake.Server) []string {
	if s == nil {
		return nil
	}
	return []string{s.URL}
}

// TestCasePackageVar checks that a client created in a package-level variable
// is traced once.
type TestCasePackageVar struct{}

func (*TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	require.NoError(t, pkgFakeErr)
	require.NoError(t, pkgErr)
}

func (*TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	perform(ctx, t, pkgClient, "package-var")
}

// ExpectedSpanCount is test.root, the elasticsearch.query span and its
// net/http span.
func (*TestCasePackageVar) ExpectedSpanCount() int {
	return 3
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				querySpan("package-var"),
				httpSpan("package-var"),
			},
		},
	}
}
