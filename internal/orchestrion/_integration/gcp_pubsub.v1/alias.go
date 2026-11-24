// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gcppubsub

import (
	"context"
	"testing"

	pubsubapi "cloud.google.com/go/pubsub/apiv1"
	"cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gapioption "google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that an admin client is traced when apiv1 and
// option are imported under other names. The options are spread, so the
// rule's variadic type has to use this file's name for the option package.
type TestCaseAliasedImport struct {
	adminBase
	srv *pstest.Server
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	tc.srv = pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, tc.srv.Close()) })
	tc.projectID = adminProject
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	opts := []gapioption.ClientOption{
		gapioption.WithEndpoint(tc.srv.Addr),
		gapioption.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		gapioption.WithoutAuthentication(),
		internaloption.SkipDialSettingsValidation(),
	}
	client, err := pubsubapi.NewSchemaClient(ctx, opts...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, client.Close()) }()

	drain(t, client.ListSchemas(ctx, &pubsubpb.ListSchemasRequest{Parent: tc.projectPath()}).Next)
}

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				tc.adminTrace("ListSchemas", tc.projectPath()),
			},
		},
	}
}

// ExpectedSpanCount is test.root, the ListSchemas span, and the gRPC client
// and server spans of the call, since pstest serves gRPC in process.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 4
}
